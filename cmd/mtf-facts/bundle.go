package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	fb "github.com/modulestf/facts/internal/bundle"
	"github.com/modulestf/facts/internal/registry"
	"github.com/spf13/cobra"
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

// newBundleCmd builds bundle.
func newBundleCmd() *cobra.Command {
	var a bundleArgs
	cmd := &cobra.Command{
		Use:     "bundle",
		Short:   bundleShort,
		Long:    bundleLong,
		Example: bundleExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(bundle(a, cmd.OutOrStdout(), cmd.ErrOrStderr()))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&a.report, "report", "", bundleReportUsage)
	f.StringVar(&a.skills, "skills", "", bundleSkillsUsage)
	f.StringVar(&a.out, "out", "", bundleOutUsage)
	f.StringVar(&a.injection, "injection", "", bundleInjectionUsage)
	f.StringVar(&a.pins.HeadSHA, "head-sha", "", bundleHeadSHAUsage)
	f.StringVar(&a.pins.MergeBaseSHA, "merge-base-sha", "", bundleMergeBaseSHAUsage)
	f.StringVar(&a.pins.BinarySHA256, "binary-sha256", "", bundleBinarySHA256Usage)
	f.StringVar(&a.handoff, "schema-handoff", "", bundleSchemaHandoffUsage)
	f.StringArrayVar(&a.schemaFiles, "schema-files", nil, bundleSchemaFilesUsage)
	f.StringVar(&a.profileDir, "profile-dir", "", bundleProfileDirUsage)
	return cmd
}

// bundle builds the facts bundle and the injection from a lint report and
// prints the mode, judgment or full. It exits 2 on bad input, a report the
// validity predicate refuses or one that does not match the registry. In
// full mode it writes no file.
func bundle(a bundleArgs, stdout, stderr io.Writer) int {
	if err := checkBundleArgs(a); err != nil {
		fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
		return exitError
	}
	res, err := buildBundle(a)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
		return exitError
	}
	if res.Mode == fb.ModeFull {
		fmt.Fprintln(stderr, "mtf-facts: bundle: full variant:", res.Reason)
	} else if err := writeOutputs(a.out, a.injection, res.Bundle, res.Injection); err != nil {
		fmt.Fprintln(stderr, "mtf-facts: bundle:", err)
		return exitError
	}
	if _, err := fmt.Fprintln(stdout, res.Mode); err != nil {
		return exitError
	}
	return 0
}

// checkBundleArgs refuses missing, malformed or clashing flag values.
func checkBundleArgs(a bundleArgs) error {
	switch {
	case a.report == "" || a.skills == "" || a.out == "" || a.injection == "":
		return errors.New("--report, --skills, --out and --injection are required")
	case filepath.Clean(a.out) == filepath.Clean(a.injection):
		return errors.New("--out and --injection must be different files")
	case !commitPattern.MatchString(a.pins.HeadSHA) || !commitPattern.MatchString(a.pins.MergeBaseSHA):
		return errors.New("--head-sha and --merge-base-sha must be 40 or 64 lowercase hex characters")
	case !sha256Pattern.MatchString(a.pins.BinarySHA256):
		return errors.New("--binary-sha256 must be 64 lowercase hex characters")
	}
	return nil
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

// writeOutputs writes the bundle and the injection. Both must be absent
// before either is written; when the second write fails, the first file is
// removed, so a failed run leaves no bundle behind.
func writeOutputs(out, injection string, b, inj []byte) error {
	for _, p := range []string{out, injection} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s exists or cannot be checked", p)
		}
	}
	if err := writeNew(out, b); err != nil {
		return err
	}
	if err := writeNew(injection, inj); err != nil {
		return errors.Join(err, os.Remove(out))
	}
	return nil
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
