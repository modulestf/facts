// Command mutate reports the guards a change adds or edits that no test
// needs. For every non-test Go file a diff touches, it makes one mutant
// per guard on a changed line (the guard never fires) and per operand of
// an && or || chain on a changed line (the operand is dropped), runs the
// tests of that file's package against each mutant through go test
// -overlay, and lists the mutants that pass every test. It only reports;
// its exit status is not zero only when it cannot run at all.
//
//	go run ./scripts/mutate -base origin/master
//
// The diff is from the merge base of -base and HEAD to the work tree, so
// uncommitted edits count; a new file counts once git knows it (git add or
// git add -N). The markdown report goes to stdout and, with
// -summary, is appended to that file.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
)

func main() {
	// An interrupt or a terminate (a CI runner cancelling the job) ends the
	// test processes with the run: each go test is in a process group of its
	// own, which the signal sent to this process does not reach.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := mainErr(ctx, os.Args[1:], os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(1)
	}
}

func mainErr(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mutate", flag.ContinueOnError)
	base := fs.String("base", "", "revision to diff against, through its merge base with HEAD")
	budget := fs.Duration("budget", 8*time.Minute, "total time for the run; mutants not started by then are not run")
	workers := fs.Int("workers", max(1, runtime.NumCPU()/2), "mutants tested at once")
	summary := fs.String("summary", "", "file to append the markdown report to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" || fs.NArg() != 0 || *workers < 1 || *budget <= 0 {
		return errUsage
	}

	root, err := git(ctx, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root = strings.TrimSpace(root)
	mb, err := git(ctx, root, "merge-base", *base, "HEAD")
	if err != nil {
		return err
	}
	// Explicit prefixes: a user's diff.mnemonicPrefix or diff.noPrefix
	// would change the b/ the parser expects.
	diff, err := git(ctx, root, "diff", "-U0", "--no-color", "--no-ext-diff", "--no-textconv",
		"--src-prefix=a/", "--dst-prefix=b/", strings.TrimSpace(mb), "--", "*.go")
	if err != nil {
		return err
	}
	changed, err := changedLines(strings.NewReader(diff))
	if err != nil {
		return err
	}

	ms, notes := collect(root, changed)

	var out bytes.Buffer
	report(&out, run(ctx, root, ms, runOptions{workers: *workers, budget: *budget}))
	if len(notes) > 0 {
		fmt.Fprintf(&out, "\nFiles skipped:\n\n%s\n", strings.Join(notes, "\n"))
	}
	if _, err := stdout.Write(out.Bytes()); err != nil {
		return err
	}
	if *summary == "" {
		return nil
	}
	f, err := os.OpenFile(*summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(out.Bytes())
	return errors.Join(werr, f.Close())
}

// collect makes the mutants of every changed file. A file the Go tool would
// not build on this platform (a build constraint, a GOOS or GOARCH suffix)
// has no test that could reach it, so it is skipped with a note, as is one
// that cannot be read or parsed.
//
// shortcut: -tags in GOFLAGS is not applied to that check, so a file behind
// a custom tag is skipped; pass the tag through build.Default.BuildTags if a
// project needs it.
func collect(root string, changed map[string]map[int]bool) ([]Mutant, []string) {
	names := make([]string, 0, len(changed))
	for n := range changed {
		names = append(names, n)
	}
	slices.Sort(names)
	var ms []Mutant
	var notes []string
	for _, n := range names {
		dir := filepath.Join(root, filepath.FromSlash(path.Dir(n)))
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(n)))
		if err != nil {
			notes = append(notes, fmt.Sprintf("- `%s` not read: %s", plain(n), plain(err.Error())))
			continue
		}
		built, err := builtHere(dir, path.Base(n), src)
		if err != nil {
			notes = append(notes, fmt.Sprintf("- `%s` not read: %s", plain(n), plain(err.Error())))
			continue
		}
		if !built {
			notes = append(notes, fmt.Sprintf("- `%s` not built on this platform", plain(n)))
			continue
		}
		got, err := mutants(n, src, changed[n])
		if err != nil {
			notes = append(notes, fmt.Sprintf("- `%s` not parsed: %s", plain(n), plain(err.Error())))
			continue
		}
		ms = append(ms, got...)
	}
	return ms, notes
}

// builtHere reports whether the Go tool would build the file on this
// platform. The file's bytes are already read, so the check takes them and
// opens nothing: the file is read once.
func builtHere(dir, name string, src []byte) (bool, error) {
	ctxt := build.Default
	ctxt.OpenFile = func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(src)), nil }
	return ctxt.MatchFile(dir, name)
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git subcommands; the revision is the caller's own
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
