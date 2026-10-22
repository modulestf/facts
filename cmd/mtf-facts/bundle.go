package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	fb "github.com/modulestf/facts/internal/bundle"
	"github.com/modulestf/facts/internal/registry"
)

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// bundleArgs are the inputs of one bundle run.
type bundleArgs struct {
	report, skills, out, injection string
	pins                           fb.Pins
	handoff, profileDir            string
	schemaFiles                    []string
}

// bundle builds the facts bundle and the injection from a lint report and
// prints the mode, judgment or full. It exits 2 on bad input, a report the
// validity predicate refuses or one that does not match the registry. In
// full mode it writes no file.
func bundle(args []string, stdout, stderr io.Writer) int {
	a, err := parseBundleArgs(args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
		}
		return exitError
	}
	res, err := buildBundle(a)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
		return exitError
	}
	if res.Mode == fb.ModeFull {
		fmt.Fprintln(stderr, "mtf-facts: bundle: full variant:", res.Reason)
	} else {
		if err := writeNew(a.out, res.Bundle); err != nil {
			fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
			return exitError
		}
		if err := writeNew(a.injection, res.Injection); err != nil {
			fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
			return exitError
		}
	}
	if _, err := fmt.Fprintln(stdout, res.Mode); err != nil {
		return exitError
	}
	return 0
}

func parseBundleArgs(args []string, stderr io.Writer) (bundleArgs, error) {
	var a bundleArgs
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&a.report, "report", "", "lint --json report")
	fs.StringVar(&a.skills, "skills", "", "reviewer skill directory at the pinned commit: references/rule-facts.md and references/engine/")
	fs.StringVar(&a.out, "out", "", "bundle file to create")
	fs.StringVar(&a.injection, "injection", "", "injected rule text file to create")
	fs.StringVar(&a.pins.HeadSHA, "head-sha", "", "workspace HEAD")
	fs.StringVar(&a.pins.MergeBaseSHA, "merge-base-sha", "", "recorded merge base")
	fs.StringVar(&a.pins.BinarySHA256, "binary-sha256", "", "pinned SHA-256 of the mtf-facts binary that wrote the report")
	fs.StringVar(&a.handoff, "schema-handoff", "", "schema handoff the lint run was given")
	fs.Func("schema-files", "a raw schema file the lint run was given; repeat for more", func(p string) error {
		a.schemaFiles = append(a.schemaFiles, p)
		return nil
	})
	fs.StringVar(&a.profileDir, "profile-dir", "", "module profile directory the lint run was given")
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	switch {
	case fs.NArg() != 0:
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case a.report == "" || a.skills == "" || a.out == "" || a.injection == "":
		return a, errors.New("--report, --skills, --out and --injection are required")
	case !commitPattern.MatchString(a.pins.HeadSHA) || !commitPattern.MatchString(a.pins.MergeBaseSHA):
		return a, errors.New("--head-sha and --merge-base-sha must be 40 or 64 lowercase hex characters")
	case !sha256Pattern.MatchString(a.pins.BinarySHA256):
		return a, errors.New("--binary-sha256 must be 64 lowercase hex characters")
	}
	return a, nil
}

// buildBundle computes the pins it can compute, checks the report and builds.
func buildBundle(a bundleArgs) (*fb.Result, error) {
	refs := filepath.Join(a.skills, "references")
	regText, err := fb.ReadRegistry(filepath.Join(refs, "rule-facts.md"))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(regText)
	a.pins.RegistrySHA256 = hex.EncodeToString(sum[:])
	reg, err := registry.Parse(string(regText))
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	if a.pins.Schema, err = fb.SchemaPins(a.handoff, a.schemaFiles); err != nil {
		return nil, err
	}
	if a.profileDir != "" {
		if a.pins.ProfileSHA256, err = fb.ProfileSHA256(a.profileDir); err != nil {
			return nil, err
		}
	}
	r, err := fb.ReadReport(a.report)
	if err != nil {
		return nil, err
	}
	if err := fb.Check(r.Envelope, a.pins); err != nil {
		return nil, err
	}
	text, err := fb.LoadEngine(filepath.Join(refs, "engine"))
	if err != nil {
		return nil, err
	}
	return fb.Build(r, reg, text, fb.Options{CapBytes: fb.DefaultCapBytes, InjectionBudget: fb.DefaultInjectionBudget})
}

// writeNew creates p, which must not exist, so a planted link or an earlier
// run's file is never written through or over.
func writeNew(p string, data []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}
