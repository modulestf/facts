// Command mtf-facts reports deterministic facts about Terraform module trees.
// It reads files only: no network, no Terraform, no other program.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/modulestf/facts/internal/facts"
)

const usage = `usage:
  mtf-facts lint <dir> [--base <dir>] [--json] [--registry <rule-facts.md>]
                 [--head-sha <sha>] [--merge-base-sha <sha>]
                                  check a module tree; with --base, only the
                                  areas the change touched
  mtf-facts rules                 list the implemented rules and their tier
  mtf-facts version               print the binary version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "rules":
		for _, r := range facts.Rules {
			fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Tier)
		}
		return 0
	case "version":
		fmt.Fprintln(stdout, version())
		return 0
	case "lint":
		return lint(args[1:], stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return 2
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// lint exits 0 with no findings, 1 with findings and 2 on an error.
// Candidates are unconfirmed and never change the exit code.
func lint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := facts.Options{Limits: facts.DefaultLimits, Version: version()}
	asJSON := fs.Bool("json", false, "write the report as JSON")
	fs.StringVar(&o.Base, "base", "", "tree the change started from")
	fs.StringVar(&o.RegistryFile, "registry", "", "rule-facts.md to hash into the output")
	fs.StringVar(&o.HeadSHA, "head-sha", "", "commit the tree was extracted from")
	fs.StringVar(&o.MergeBaseSHA, "merge-base-sha", "", "commit the base tree was extracted from")
	var dirs []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		dirs, rest = append(dirs, fs.Arg(0)), fs.Args()[1:]
	}
	if len(dirs) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	o.Head = dirs[0]
	sum, err := binarySHA256()
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return 2
	}
	o.BinarySHA256 = sum
	r, err := facts.Run(o)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return 2
	}
	var out []byte
	if *asJSON {
		out, err = json.MarshalIndent(r, "", "  ")
		out = append(out, '\n')
	} else {
		out = []byte(human(r))
	}
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return 2
	}
	if len(out) > o.Limits.MaxOutputBytes {
		fmt.Fprintf(stderr, "mtf-facts: output over %d bytes\n", o.Limits.MaxOutputBytes)
		return 2
	}
	if _, err := stdout.Write(out); err != nil {
		return 2
	}
	if len(r.Findings) > 0 {
		return 1
	}
	return 0
}

func human(r *facts.Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		loc := f.File
		if f.Line != nil {
			loc = fmt.Sprintf("%s:%d", f.File, *f.Line)
		}
		fmt.Fprintf(&b, "%s: %s %s: %s\n  fix: %s\n", loc, f.Severity, f.RuleID, f.Summary, f.SuggestedFix)
	}
	for _, c := range r.Candidates {
		loc := c.File
		if c.Line != nil {
			loc = fmt.Sprintf("%s:%d", c.File, *c.Line)
		}
		fmt.Fprintf(&b, "%s: unconfirmed %s: %s, confirm or reject\n", loc, c.RuleID, c.Subject)
	}
	counts := map[string]int{}
	for _, e := range r.Manifest {
		counts[e.Status]++
		if e.Status == facts.StatusNotEvaluated {
			fmt.Fprintf(&b, "not evaluated: %s in %s: %s\n", e.RuleID, e.Area, e.Reason)
		}
	}
	fmt.Fprintf(&b, "%d findings, %d candidates; manifest: %d finding, %d candidate, %d clean, %d not_evaluated, %d not_applicable\n",
		len(r.Findings), len(r.Candidates), counts[facts.StatusFinding], counts[facts.StatusCandidate],
		counts[facts.StatusClean], counts[facts.StatusNotEvaluated], counts[facts.StatusNotApplicable])
	reg := "none"
	if r.Envelope.RegistrySHA256 != nil {
		reg = *r.Envelope.RegistrySHA256
	}
	fmt.Fprintf(&b, "mtf-facts %s, binary sha256 %s, registry sha256 %s\n", r.Envelope.Tool.Version, r.Envelope.Tool.BinarySHA256, reg)
	return b.String()
}

func binarySHA256() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("binary digest: %w", err)
	}
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("binary digest: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("binary digest: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
