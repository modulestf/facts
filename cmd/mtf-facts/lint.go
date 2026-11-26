package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/stats"
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
		view    textView
		asJSON  bool
		ai      bool
		timeout time.Duration
		statsTo string
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
			return withStats(cmd, args, statsTo, func(rec *stats.Recorder) int {
				o.Limits.Stats = rec
				return lint(cmd, o, view, asJSON, ai, timeout)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", lintBaseUsage)
	f.StringVar(&o.Diff, "diff", "", lintDiffUsage)
	f.BoolVar(&o.FullTree, "full-tree", false, lintFullTreeUsage)
	f.BoolVar(&asJSON, "json", false, lintJSONUsage)
	f.BoolVar(&ai, "ai", false, lintAIUsage)
	f.BoolVar(&view.candidates, "candidates", false, lintCandidatesUsage)
	f.BoolVar(&view.notEvaluated, "not-evaluated", false, lintNotEvaluatedUsage)
	addTimeoutFlag(cmd, &timeout)
	f.StringVar(&o.RegistryFile, "registry", "", lintRegistryUsage)
	f.StringVar(&o.ProfileDir, "profile-dir", "", lintProfileDirUsage)
	f.StringArrayVar(&o.SchemaFiles, "schema-files", nil, lintSchemaFilesUsage)
	f.StringVar(&o.SchemaHandoff, "schema-handoff", "", lintSchemaHandoffUsage)
	f.StringVar(&o.HeadSHA, "head-sha", "", lintHeadSHAUsage)
	f.StringVar(&o.MergeBaseSHA, "merge-base-sha", "", lintMergeBaseSHAUsage)
	addStatsFlag(cmd, &statsTo)
	cmd.MarkFlagsMutuallyExclusive("base", "diff")
	return cmd
}

// lint returns one of the exit codes above. Candidates are unconfirmed and
// never change the exit code.
func lint(cmd *cobra.Command, o facts.Options, view textView, asJSON, ai bool, timeout time.Duration) int {
	o.Version = version()
	// Read the engine here: a worker the timer abandons outlives this call
	// and must not read runFacts after it returns.
	engine := runFacts
	return runBounded(cmd, timeout, &o.Limits, func() workResult { return lintWork(engine, o, view, asJSON, ai) }, nil)
}

// runFacts is the engine entry point; tests replace it.
var runFacts = facts.Run

// lintWork runs engine and renders the report, the AI form of the JSON
// report when ai is set, or the text report view shapes. It writes
// nothing.
func lintWork(engine func(facts.Options) (*facts.Report, error), o facts.Options, view textView, asJSON, ai bool) workResult {
	o.Limits.Stats.Phase("binary_hash")
	sum, err := binarySHA256()
	if err != nil {
		return workResult{err: err}
	}
	o.BinarySHA256 = sum
	r, err := engine(o)
	if err != nil {
		return workResult{err: err}
	}
	o.Limits.Stats.Phase("render")
	var out []byte
	switch {
	case asJSON && ai:
		// One line: indentation is a third of the bytes a reader pays for.
		if out, err = json.Marshal(r.AIReport()); err != nil {
			return workResult{err: err}
		}
		out = append(out, '\n')
	case asJSON:
		return jsonResult(r, exitCode(r))
	default:
		view.profileGiven = o.ProfileDir != ""
		view.schemaGiven = len(o.SchemaFiles) > 0 || o.SchemaHandoff != ""
		out = []byte(human(r, view))
	}
	return workResult{out: out, code: exitCode(r)}
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

// writeScope writes the scope line: how the change was given, and how many
// areas the run checked, reached only as callers, or left out, naming those.
func writeScope(b *strings.Builder, s *facts.Scope) {
	switch {
	case s.Change == facts.ChangeNone:
		fmt.Fprintf(b, "scope: no change; %d areas checked\n", len(s.AreasChecked))
	case s.FullTree:
		fmt.Fprintf(b, "scope: change from %s, full tree; %d areas checked\n", s.Change, len(s.AreasChecked))
	default:
		fmt.Fprintf(b, "scope: change from %s; %d areas checked, %d calling a changed module, %d left out", s.Change,
			len(s.AreasChecked), len(s.AreasCalling), len(s.AreasLeftOut))
		if len(s.AreasLeftOut) > 0 {
			fmt.Fprintf(b, ": %s (--full-tree checks every area)", clean(strings.Join(s.AreasLeftOut, ", ")))
		}
		b.WriteString("\n")
	}
}

// writeCallers writes each changed module with the floors the change moves,
// and each caller with its own floor at those addresses and the version floor
// candidates filed in its area. The JSON report has every floor.
func writeCallers(b *strings.Builder, mods []facts.ChangedModule) {
	for _, m := range mods {
		base := map[string]string{}
		for _, f := range m.BaseFloors {
			base[f.Address] = constraint(f)
		}
		var moved, addrs []string
		for _, f := range m.HeadFloors {
			was, ok := base[f.Address]
			switch {
			case m.BaseFloors == nil:
				moved = append(moved, f.Address+" "+constraint(f))
			case !ok || was != constraint(f):
				moved = append(moved, f.Address+" "+cmp.Or(was, "none")+" -> "+constraint(f))
			default:
				continue
			}
			addrs = append(addrs, f.Address)
		}
		fmt.Fprintf(b, "changed module %s (%s): %s\n", clean(m.Module), strings.Join(m.Changed, ", "),
			clean(cmp.Or(strings.Join(moved, ", "), "floors unchanged")))
		for _, c := range m.Callers {
			own := map[string]string{}
			for _, f := range c.Floors {
				own[f.Address] = constraint(f)
			}
			var parts []string
			for _, a := range addrs {
				parts = append(parts, a+" "+cmp.Or(own[a], "none"))
			}
			if len(c.Candidates) > 0 {
				parts = append(parts, "candidates "+strings.Join(c.Candidates, " "))
			}
			line := fmt.Sprintf("  called from %s:%d", c.File, c.Line)
			if len(parts) > 0 {
				line += ": " + strings.Join(parts, ", ")
			}
			fmt.Fprintf(b, "%s\n", clean(line))
		}
	}
}

func constraint(f facts.Floor) string {
	if f.Constraint == nil {
		return "no version"
	}
	return *f.Constraint
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
