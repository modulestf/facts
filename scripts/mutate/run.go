package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	statusSurvived    = "survived"
	statusKilled      = "killed"
	statusNotCompiled = "not compiled"
	statusTimedOut    = "timed out"
	statusNotRun      = "not run"
	statusBaseline    = "baseline failed"
	statusNoTests     = "no test files"
)

// minTimeout is the least time a mutant's tests get; a mutant gets three
// times its package's unmutated run when that is longer.
const minTimeout = 30 * time.Second

type runOptions struct {
	workers int
	budget  time.Duration
	// floor replaces minTimeout when set; tests use it to make a timeout
	// short.
	floor time.Duration
}

type result struct {
	Mutant
	Status string
}

type baseline struct {
	took time.Duration
	ok   bool
	ran  bool
	// noTests: the package has no test file, so a pass proves nothing.
	noTests bool
}

// run tests each mutant against the tests of its own package, through a
// go test -overlay that swaps the mutated file in, so the checkout is never
// written and mutants run side by side. Each package first runs once
// unmutated; a package whose tests fail then has no mutant run. A mutant
// not started before the budget ends, or stopped by it, is not run. A
// package with no test file has no verdict.
func run(ctx context.Context, root string, ms []Mutant, opts runOptions) []result {
	ctx, cancel := context.WithTimeout(ctx, opts.budget)
	defer cancel()
	workers := max(opts.workers, 1)
	floor := cmp.Or(opts.floor, minTimeout)

	byPkg := map[string][]int{}
	for i, m := range ms {
		dir := path.Dir(m.File)
		byPkg[dir] = append(byPkg[dir], i)
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)

	base := make(map[string]baseline, len(pkgs))
	var mu sync.Mutex
	parallel(workers, len(pkgs), func(i int) {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		// The rest of the budget is the test timeout, so a hung baseline
		// test binary ends itself even if the process group is not killed.
		out, err := goTest(ctx, root, pkgs[i], "", remaining(ctx))
		b := baseline{
			took: time.Since(start), ok: err == nil, ran: ctx.Err() == nil,
			noTests: err == nil && bytes.Contains(out, []byte("[no test files]")),
		}
		mu.Lock()
		base[pkgs[i]] = b
		mu.Unlock()
	})

	// Fast packages first, so a slow one spends the end of the budget.
	slices.SortStableFunc(pkgs, func(a, b string) int { return cmp.Compare(base[a].took, base[b].took) })
	res := make([]result, len(ms))
	var order []int
	for _, p := range pkgs {
		for _, i := range byPkg[p] {
			res[i] = result{Mutant: ms[i], Status: statusNotRun}
			switch b := base[p]; {
			case !b.ran:
			case !b.ok:
				res[i].Status = statusBaseline
			case b.noTests:
				res[i].Status = statusNoTests
			default:
				order = append(order, i)
			}
		}
	}

	tmp, err := os.MkdirTemp("", "mutate-")
	if err != nil {
		return res
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	parallel(workers, len(order), func(j int) {
		i := order[j]
		if ctx.Err() != nil {
			return
		}
		p := path.Dir(ms[i].File)
		res[i].Status = runOne(ctx, root, tmp, j, ms[i], max(3*base[p].took, floor), floor)
	})
	return res
}

func runOne(ctx context.Context, root, tmp string, n int, m Mutant, timeout, grace time.Duration) string {
	src := filepath.Join(tmp, fmt.Sprintf("m%d.go", n))
	ov := filepath.Join(tmp, fmt.Sprintf("m%d.json", n))
	data, err := json.Marshal(map[string]map[string]string{
		"Replace": {filepath.Join(root, filepath.FromSlash(m.File)): src},
	})
	if err != nil || os.WriteFile(src, m.Src, 0o600) != nil || os.WriteFile(ov, data, 0o600) != nil {
		return statusNotRun
	}
	mctx, cancel := context.WithTimeout(ctx, timeout+grace)
	defer cancel()
	out, err := goTest(mctx, root, path.Dir(m.File), ov, timeout)
	switch {
	case ctx.Err() != nil:
		return statusNotRun
	case mctx.Err() != nil, bytes.Contains(out, []byte("panic: test timed out")):
		return statusTimedOut
	case err == nil:
		return statusSurvived
	case bytes.Contains(out, []byte("[build failed]")), bytes.Contains(out, []byte("[setup failed]")):
		return statusNotCompiled
	}
	return statusKilled
}

// remaining is the time left before ctx's deadline, or 0 with no deadline.
func remaining(ctx context.Context) time.Duration {
	d, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	return max(time.Until(d), time.Millisecond)
}

// goTest runs the tests of one package directory, failing fast, with no
// vet and no cached result. The go command and the test binary it starts
// are one process group, ended together when ctx ends.
func goTest(ctx context.Context, root, dir, overlay string, timeout time.Duration) ([]byte, error) {
	args := []string{"test", "-count=1", "-failfast", "-vet=off"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	if timeout > 0 {
		args = append(args, "-timeout="+timeout.String())
	}
	pkg := "./" + dir
	if dir == "." {
		pkg = "."
	}
	cmd := exec.CommandContext(ctx, "go", append(args, pkg)...) //nolint:gosec // G204: go test of a package directory of this checkout
	cmd.Dir = root
	cmd.WaitDelay = 10 * time.Second
	ownProcessGroup(cmd)
	return cmd.CombinedOutput()
}

// parallel calls f for 0..n-1 on at most workers goroutines.
func parallel(workers, n int, f func(int)) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, n) {
		wg.Go(func() {
			for i := range jobs {
				f(i)
			}
		})
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// report writes the markdown summary: the counts, a table of survivors and
// a list of the mutants with no verdict.
func report(w io.Writer, res []result) {
	fmt.Fprintln(w, "## Surviving mutants on the changed lines")
	fmt.Fprintln(w)
	if len(res) == 0 {
		fmt.Fprintln(w, "No guard or condition operand on the changed lines.")
		return
	}
	slices.SortStableFunc(res, func(a, b result) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	counts := map[string]int{}
	for _, r := range res {
		counts[r.Status]++
	}
	var parts []string
	for _, s := range []string{statusSurvived, statusKilled, statusNotCompiled, statusTimedOut, statusNotRun, statusBaseline, statusNoTests} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	fmt.Fprintf(w, "%d mutants on the changed lines: %s.\n\n", len(res), strings.Join(parts, ", "))
	fmt.Fprintln(w, "A survivor passes every test of its package: a guard removed, or a condition operand dropped, and no test fails. Report only; nothing here fails the build.")
	if counts[statusSurvived] > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Where | Mutation | Code |")
		fmt.Fprintln(w, "| --- | --- | --- |")
		for _, r := range res {
			if r.Status == statusSurvived {
				fmt.Fprintf(w, "| `%s:%d` | %s | `%s` |\n", plain(r.File), r.Line, r.Kind, cell(r.Code))
			}
		}
	}
	first := true
	for _, r := range res {
		if r.Status == statusSurvived || r.Status == statusKilled || r.Status == statusNotCompiled {
			continue
		}
		if first {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "No verdict:")
			fmt.Fprintln(w)
			first = false
		}
		fmt.Fprintf(w, "- `%s:%d` %s: %s\n", plain(r.File), r.Line, r.Kind, r.Status)
	}
}

// plain makes text safe in a code span in a table cell or a list item: a
// backtick would close the span, a bar would split the cell and a line break
// would end the item. File names and error messages hold text a change
// controls, so they pass through here before they reach the report.
func plain(s string) string {
	return strings.NewReplacer("`", "'", "|", `\|`, "\n", " ", "\r", " ").Replace(s)
}

// cell is plain, cut to a length a table cell can hold.
func cell(s string) string {
	const limit = 100
	if len(s) > limit {
		s = s[:limit] + "..."
	}
	return plain(s)
}

var errUsage = errors.New("usage: go run ./scripts/mutate -base <revision> [-budget 8m] [-workers n] [-summary file]")
