package main

import (
	"fmt"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schemadiff"
	"github.com/modulestf/facts/internal/stats"
	"github.com/spf13/cobra"
)

// newSchemaDiffCmd builds schema-diff.
func newSchemaDiffCmd() *cobra.Command {
	var (
		o       = schemadiff.Options{Limits: facts.DefaultLimits}
		timeout time.Duration
		statsTo string
	)
	cmd := &cobra.Command{
		Use:     "schema-diff",
		Short:   schemaDiffShort,
		Long:    schemaDiffLong,
		Example: schemaDiffExample,
		GroupID: groupCheck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStats(cmd, args, statsTo, func(rec *stats.Recorder) int {
				o.Limits.Stats = rec
				return schemaDiff(cmd, o, timeout)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.From, "from", "", schemaDiffFromUsage)
	f.StringVar(&o.To, "to", "", schemaDiffToUsage)
	f.StringVar(&o.TypesFrom, "types-from", "", schemaDiffTypesFromUsage)
	addTimeoutFlag(cmd, &timeout)
	addStatsFlag(cmd, &statsTo)
	return cmd
}

// schemaDiff compares two provider schema files for the types a module tree
// uses and writes the report as JSON. It exits 0 with the report, changes or
// not, and 2 on bad input, a limit or the timeout.
func schemaDiff(cmd *cobra.Command, o schemadiff.Options, timeout time.Duration) int {
	if o.From == "" || o.To == "" || o.TypesFrom == "" {
		return usageError(cmd, schemaDiffRequired)
	}
	return runBounded(cmd, timeout, &o.Limits, func() workResult {
		o.Limits.Stats.Phase("binary_hash")
		sum, err := binarySHA256()
		if err != nil {
			return workResult{err: err}
		}
		o.Tool = schemadiff.Tool{Name: "mtf-facts", Version: version(), BinarySHA256: sum}
		r, err := schemadiff.Run(o)
		if err != nil {
			return workResult{err: fmt.Errorf("schema-diff: %w", err)}
		}
		o.Limits.Stats.Phase("render")
		return jsonResult(r, exitClean)
	}, nil)
}
