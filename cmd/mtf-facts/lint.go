package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// Exit codes of lint.
const (
	exitClean        = 0 // no findings, every rule decided
	exitFindings     = 1 // at least one finding
	exitError        = 2 // bad input, a limit, the deadline or an integrity failure
	exitNotEvaluated = 3 // no findings, but some (rule, area) was not evaluated
)

// newLintCmd builds lint, which checks the module tree in its argument.
func newLintCmd() *cobra.Command {
	var (
		o       = facts.Options{Limits: facts.DefaultLimits}
		asJSON  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "lint <dir>",
		Short:   lintShort,
		Long:    lintLong,
		Example: lintExample,
		GroupID: groupCheck,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			return status(lint(cmd, o, asJSON, timeout))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", lintBaseUsage)
	f.BoolVar(&asJSON, "json", false, lintJSONUsage)
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	f.StringVar(&o.RegistryFile, "registry", "", lintRegistryUsage)
	f.StringVar(&o.ProfileDir, "profile-dir", "", lintProfileDirUsage)
	f.StringArrayVar(&o.SchemaFiles, "schema-files", nil, lintSchemaFilesUsage)
	f.StringVar(&o.SchemaHandoff, "schema-handoff", "", lintSchemaHandoffUsage)
	f.StringVar(&o.HeadSHA, "head-sha", "", lintHeadSHAUsage)
	f.StringVar(&o.MergeBaseSHA, "merge-base-sha", "", lintMergeBaseSHAUsage)
	return cmd
}

// lint returns one of the exit codes above. Candidates are unconfirmed and
// never change the exit code.
func lint(cmd *cobra.Command, o facts.Options, asJSON bool, timeout time.Duration) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	o.Version = version()
	// Defense in depth only: the timer below is the timeout mechanism.
	o.Limits.Deadline = time.Now().Add(timeout)

	done := make(chan lintResult, 1) // buffered: a late worker never blocks
	go func() { done <- lintWork(o, asJSON) }()
	timer := time.NewTimer(timeout)
	var res lintResult
	select {
	case res = <-done:
		timer.Stop()
	case <-timer.C:
		// main's os.Exit ends the worker, which may be stuck in a parser.
		fmt.Fprint(stderr, "mtf-facts: timeout\n")
		return exitError
	}
	if errors.Is(res.err, facts.ErrDeadline) {
		// The engine's own deadline can win the race with the timer.
		fmt.Fprint(stderr, "mtf-facts: timeout\n")
		return exitError
	}
	if res.err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", res.err)
		return exitError
	}
	if _, err := stdout.Write(res.out); err != nil {
		return exitError
	}
	return res.code
}

// runFacts is the engine entry point; tests replace it.
var runFacts = facts.Run

// lintResult is what the lint worker hands back. Only lint writes output.
type lintResult struct {
	out  []byte
	code int
	err  error
}

// lintWork runs the engine and renders the report. It writes nothing.
func lintWork(o facts.Options, asJSON bool) lintResult {
	sum, err := binarySHA256()
	if err != nil {
		return lintResult{err: err}
	}
	o.BinarySHA256 = sum
	r, err := runFacts(o)
	if err != nil {
		return lintResult{err: err}
	}
	var out []byte
	if asJSON {
		if out, err = json.MarshalIndent(r, "", "  "); err != nil {
			return lintResult{err: err}
		}
		out = append(out, '\n')
	} else {
		out = []byte(human(r))
	}
	if len(out) > o.Limits.MaxOutputBytes {
		return lintResult{err: fmt.Errorf("output over %d bytes", o.Limits.MaxOutputBytes)}
	}
	return lintResult{out: out, code: exitCode(r)}
}

func exitCode(r *facts.Report) int {
	if len(r.Findings) > 0 {
		return exitFindings
	}
	for _, e := range r.Manifest {
		if e.Status == facts.StatusNotEvaluated {
			return exitNotEvaluated
		}
	}
	return exitClean
}

func human(r *facts.Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		loc := f.File
		if f.Line != nil {
			loc = fmt.Sprintf("%s:%d", f.File, *f.Line)
		}
		fmt.Fprintf(&b, "%s: %s %s: %s\n  fix: %s\n", clean(loc), f.Severity, f.RuleID, clean(f.Summary), clean(f.SuggestedFix))
	}
	for _, c := range r.Candidates {
		loc := c.File
		if c.Line != nil {
			loc = fmt.Sprintf("%s:%d", c.File, *c.Line)
		}
		fmt.Fprintf(&b, "%s: unconfirmed %s: %s, confirm or reject\n", clean(loc), c.RuleID, clean(c.Subject))
	}
	counts := map[string]int{}
	for _, e := range r.Manifest {
		counts[e.Status]++
		if e.Status == facts.StatusNotEvaluated {
			fmt.Fprintf(&b, "not evaluated: %s in %s: %s\n", e.RuleID, clean(e.Area), clean(e.Reason))
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

// clean makes a string from the tree safe for a terminal: control and
// non-ASCII bytes are escaped and the length is capped.
func clean(s string) string {
	const maxLen = 300
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if b.Len() >= maxLen {
			b.WriteString("...")
			break
		}
		if c := s[i]; c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&b, "\\x%02x", c)
		} else {
			b.WriteByte(c)
		}
	}
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
