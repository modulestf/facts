package main

import (
	"fmt"
	"io"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

func newRulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rules",
		Short:   rulesShort,
		Long:    rulesLong,
		GroupID: groupOther,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(rules(cmd.OutOrStdout()))
		},
	}
}

// rules lists every implemented rule and its tier, one per line, sorted by id.
func rules(stdout io.Writer) int {
	for _, r := range facts.Rules {
		fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Tier)
	}
	return 0
}
