package main

import (
	"encoding/json"
	"fmt"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schemadiff"
	"github.com/spf13/cobra"
)

// newSchemaDiffCmd builds schema-diff.
func newSchemaDiffCmd() *cobra.Command {
	o := schemadiff.Options{Limits: facts.DefaultLimits}
	cmd := &cobra.Command{
		Use:     "schema-diff",
		Short:   schemaDiffShort,
		Long:    schemaDiffLong,
		Example: schemaDiffExample,
		GroupID: groupCheck,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(schemaDiff(cmd, o))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.From, "from", "", schemaDiffFromUsage)
	f.StringVar(&o.To, "to", "", schemaDiffToUsage)
	f.StringVar(&o.TypesFrom, "types-from", "", schemaDiffTypesFromUsage)
	return cmd
}

// schemaDiff compares two provider schema files for the types a module tree
// uses and writes the report as JSON. It exits 0 with the report, changes or
// not, and 2 on bad input or a limit.
func schemaDiff(cmd *cobra.Command, o schemadiff.Options) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if o.From == "" || o.To == "" || o.TypesFrom == "" {
		return usageError(cmd, schemaDiffRequired)
	}
	sum, err := binarySHA256()
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	o.Tool = schemadiff.Tool{Name: "mtf-facts", Version: version(), BinarySHA256: sum}
	r, err := schemadiff.Run(o)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts: schema-diff:", err)
		return exitError
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if len(out) > o.Limits.MaxOutputBytes {
		fmt.Fprintf(stderr, "mtf-facts: output over %d bytes\n", o.Limits.MaxOutputBytes)
		return exitError
	}
	if _, err := stdout.Write(append(out, '\n')); err != nil {
		return exitError
	}
	return 0
}
