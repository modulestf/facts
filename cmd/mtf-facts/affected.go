package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/stats"
	"github.com/spf13/cobra"
)

// newAffectedCmd builds affected, which lists the examples a change can
// affect.
func newAffectedCmd() *cobra.Command {
	var (
		o       = facts.AffectedOptions{Limits: facts.DefaultLimits}
		asJSON  bool
		timeout time.Duration
		statsTo string
	)
	cmd := &cobra.Command{
		Use:     "affected <dir>",
		Short:   affectedShort,
		Long:    affectedLong,
		Example: affectedExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			return withStats(cmd, args, statsTo, func(rec *stats.Recorder) int {
				if cmd.Flags().Changed("module-root") && o.ModuleRoot == "" {
					return usageError(cmd, "--module-root names no module root")
				}
				o.Limits.Stats = rec
				return affected(cmd, o, asJSON, timeout)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", affectedBaseUsage)
	f.StringVar(&o.Diff, "diff", "", affectedDiffUsage)
	f.StringVar(&o.ModuleRoot, "module-root", "", affectedModuleRootUsage)
	f.BoolVar(&asJSON, "json", false, affectedJSONUsage)
	addTimeoutFlag(cmd, &timeout)
	addStatsFlag(cmd, &statsTo)
	cmd.MarkFlagsMutuallyExclusive("base", "diff")
	return cmd
}

// affected returns exitClean or exitError, as lint does with its timeout.
func affected(cmd *cobra.Command, o facts.AffectedOptions, asJSON bool, timeout time.Duration) int {
	return runBounded(cmd, timeout, &o.Limits, func() workResult { return affectedWork(o, asJSON) }, nil)
}

func affectedWork(o facts.AffectedOptions, asJSON bool) workResult {
	r, err := facts.Affected(o)
	if err != nil {
		return workResult{err: err}
	}
	o.Limits.Stats.Phase("render")
	if asJSON {
		return jsonResult(r, exitClean)
	}
	var b strings.Builder
	for _, e := range r.Examples {
		if e.Unresolved {
			fmt.Fprintf(&b, "%s %s unresolved\n", e.Reason, e.Dir)
		} else {
			fmt.Fprintf(&b, "%s %s\n", e.Reason, e.Dir)
		}
	}
	fb := "none"
	if r.Fallback != nil {
		fb = *r.Fallback
	}
	fmt.Fprintf(&b, "inert: %v, fallback: %s, %d changed paths\n", r.Inert, fb, len(r.Changed))
	return workResult{out: []byte(b.String())}
}
