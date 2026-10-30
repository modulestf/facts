package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modulestf/facts/internal/fixture"
	"github.com/spf13/cobra"
)

// newFixtureCheckCmd builds fixture-check.
func newFixtureCheckCmd() *cobra.Command {
	var (
		o      fixture.Options
		pins   string
		expect string
		asJSON bool
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
			return status(fixtureCheck(cmd, o, pins, expect, asJSON))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Level, "level", "L1", fixtureCheckLevelUsage+" ("+strings.Join(fixture.Levels(), ", ")+")")
	f.StringVar(&o.Resource, "resource", "", fixtureCheckResourceUsage)
	f.StringVar(&pins, "pins", "", fixtureCheckPinsUsage)
	f.StringVar(&expect, "expect", "", fixtureCheckExpectUsage)
	f.BoolVar(&asJSON, "json", false, fixtureCheckJSONUsage)
	return cmd
}

// fixtureCheck checks one scratch tree and writes the result. It exits 0
// when the tree passes, 1 with violations and 2 on bad input.
func fixtureCheck(cmd *cobra.Command, o fixture.Options, pins, expect string, asJSON bool) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if pins != "" {
		p, err := fixture.ReadPins(pins)
		if err != nil {
			fmt.Fprintln(stderr, "mtf-facts: fixture-check:", err)
			return exitError
		}
		o.Pins = p
	}
	if expect != "" {
		m, err := fixture.ReadManifest(expect)
		if err != nil {
			fmt.Fprintln(stderr, "mtf-facts: fixture-check:", err)
			return exitError
		}
		o.Expect = m
	}
	r, err := fixture.Check(o)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts: fixture-check:", err)
		return exitError
	}
	var out []byte
	if asJSON {
		if out, err = json.MarshalIndent(r, "", "  "); err != nil {
			fmt.Fprintln(stderr, "mtf-facts:", err)
			return exitError
		}
		out = append(out, '\n')
	} else {
		var b strings.Builder
		for _, v := range r.Violations {
			fmt.Fprintf(&b, "%s:%d: %s\n", clean(v.File), v.Line, strings.TrimSpace(v.Rule+" "+v.Detail))
		}
		fmt.Fprintf(&b, "%d violations, tree sha256 %s\n", len(r.Violations), r.TreeSHA256)
		out = []byte(b.String())
	}
	if _, err := stdout.Write(out); err != nil {
		return exitError
	}
	if !r.OK {
		return exitFindings
	}
	return exitClean
}
