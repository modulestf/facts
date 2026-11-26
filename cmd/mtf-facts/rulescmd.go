package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// rulesSchemaVersion is the schema_version of the rules --json report,
// schema/rules.schema.json.
const rulesSchemaVersion = 1

// ruleJSON is one rule of rules --json: the Rule struct without its check,
// with the registry's names for the fields the registry also has.
type ruleJSON struct {
	ID                string   `json:"id"`
	Tier              string   `json:"tier"`
	Class             string   `json:"class"`
	Scope             string   `json:"scope"`
	AreaKinds         []string `json:"area_kinds"`
	CrossArea         bool     `json:"cross_area"`
	NeedsBase         bool     `json:"needs_base"`
	ReadsUnreadConfig bool     `json:"reads_unread_config"`
}

func newRulesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "rules",
		Short:   rulesShort,
		Long:    rulesLong,
		GroupID: groupOther,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return status(rulesJSON(cmd.OutOrStdout(), cmd.ErrOrStderr()))
			}
			return status(rules(cmd.OutOrStdout()))
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, rulesJSONUsage)
	return cmd
}

// rules lists every implemented rule and its tier, one per line, sorted by id.
func rules(stdout io.Writer) int {
	for _, r := range facts.Rules {
		fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Tier)
	}
	return 0
}

// rulesJSON writes every implemented rule, sorted by id, as one JSON object.
func rulesJSON(stdout, stderr io.Writer) int {
	out := struct {
		SchemaVersion int        `json:"schema_version"`
		Rules         []ruleJSON `json:"rules"`
	}{SchemaVersion: rulesSchemaVersion, Rules: make([]ruleJSON, 0, len(facts.Rules))}
	for _, r := range facts.Rules {
		kinds := slices.Sorted(slices.Values(r.Kinds))
		if kinds == nil {
			kinds = []string{}
		}
		out.Rules = append(out.Rules, ruleJSON{ID: r.ID, Tier: r.Tier, Class: r.Class, Scope: r.Scope, AreaKinds: kinds,
			CrossArea: r.CrossArea, NeedsBase: r.NeedsBase, ReadsUnreadConfig: r.ReadsUnreadConfig})
	}
	slices.SortFunc(out.Rules, func(a, b ruleJSON) int { return strings.Compare(a.ID, b.ID) })
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if _, err := stdout.Write(append(data, '\n')); err != nil {
		return exitError
	}
	return exitClean
}
