package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/fixture"
	"github.com/modulestf/facts/internal/stats"
	"github.com/spf13/cobra"
)

// newFixtureCheckCmd builds fixture-check.
func newFixtureCheckCmd() *cobra.Command {
	var (
		o       fixture.Options
		in      fixtureInputs
		asJSON  bool
		timeout time.Duration
		statsTo string
	)
	cmd := &cobra.Command{
		Use:     "fixture-check <dir>",
		Short:   fixtureCheckShort,
		Long:    fixtureCheckLong,
		Example: fixtureCheckExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Dir = args[0]
			return withStats(cmd, args, statsTo, func(rec *stats.Recorder) int {
				o.Stats = rec
				return fixtureCheck(cmd, o, in, asJSON, timeout)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Level, "level", "L1", fixtureCheckLevelUsage+" ("+strings.Join(fixture.Levels(), ", ")+")")
	f.StringVar(&o.Resource, "resource", "", fixtureCheckResourceUsage)
	f.StringVar(&in.pins, "pins", "", fixtureCheckPinsUsage)
	f.StringVar(&in.expect, "expect", "", fixtureCheckExpectUsage)
	f.StringVar(&in.schema, "schema", "", fixtureCheckSchemaUsage)
	f.BoolVar(&asJSON, "json", false, fixtureCheckJSONUsage)
	addTimeoutFlag(cmd, &timeout)
	addStatsFlag(cmd, &statsTo)
	return cmd
}

// fixtureCheck checks one scratch tree and writes the result. It exits 0
// when the tree passes, 1 with violations and 2 on bad input, the output cap
// or the timeout.
func fixtureCheck(cmd *cobra.Command, o fixture.Options, in fixtureInputs, asJSON bool, timeout time.Duration) int {
	lim := facts.DefaultLimits
	lim.Stats = o.Stats
	return runBounded(cmd, timeout, &lim, func() workResult {
		r, err := fixtureCheckWork(o, in)
		if err != nil {
			return workResult{err: fmt.Errorf("fixture-check: %w", err)}
		}
		o.Stats.Phase("render")
		code := exitClean
		if !r.OK {
			code = exitFindings
		}
		if asJSON {
			return jsonResult(r, code)
		}
		var b strings.Builder
		for _, v := range r.Violations {
			fmt.Fprintf(&b, "%s:%d: %s\n", clean(v.File), v.Line, strings.TrimSpace(v.Rule+" "+v.Detail))
		}
		fmt.Fprintf(&b, "%d violations, tree sha256 %s\n", len(r.Violations), r.TreeSHA256)
		return workResult{out: []byte(b.String()), code: code}
	}, nil)
}

// fixtureInputs are the files fixture-check reads beside the tree.
type fixtureInputs struct{ pins, expect, schema string }

// fixtureCheckWork reads the input files and checks the tree.
func fixtureCheckWork(o fixture.Options, in fixtureInputs) (*fixture.Result, error) {
	var err error
	if in.pins != "" {
		if o.Pins, err = fixture.ReadPins(in.pins); err != nil {
			return nil, err
		}
	}
	if in.expect != "" {
		if o.Expect, err = fixture.ReadManifest(in.expect); err != nil {
			return nil, err
		}
	}
	if in.schema != "" {
		if o.Schemas, err = fixture.ReadSchemas(in.schema); err != nil {
			return nil, err
		}
	}
	return fixture.Check(o)
}
