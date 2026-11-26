package main

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/fixture"
	"github.com/modulestf/facts/internal/schemadiff"
	"github.com/modulestf/facts/internal/stats"
)

// versionSchemaVersion is the schema_version of the version --json report,
// schema/version.schema.json. A new field keeps it; a changed meaning of a
// field raises it.
const versionSchemaVersion = 1

// versionReport is what version --json prints.
type versionReport struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	Commit        *string        `json:"commit"`
	Modified      *bool          `json:"modified"`
	Go            string         `json:"go"`
	Reports       map[string]int `json:"reports"`
	Features      []string       `json:"features"`
}

// reportVersions is the schema_version of each JSON report a command writes
// at its top level, keyed by the command that writes it; lint's is the
// envelope's. stats is the record --stats-file writes, whatever the command.
// bundle is not listed: its schema_version versions only the optional
// ownership unit, not the bundle.
func reportVersions() map[string]int {
	return map[string]int{
		"affected":      facts.AffectedSchemaVersion,
		"behaviour":     behaviour.SchemaVersion,
		"changes":       facts.ChangesSchemaVersion,
		"claims-eval":   behaviour.SchemaVersion,
		"fixture-check": fixture.SchemaVersion,
		"lint":          facts.SchemaVersion,
		"schema-diff":   schemadiff.SchemaVersion,
		"stats":         stats.SchemaVersion,
	}
}

// behaviourFeatures names what a command does that no command or flag
// name shows. A name, once shipped, keeps its meaning and is never removed
// (testdata/features.txt holds every shipped name). The test that pins the
// behaviour is named beside each entry.
var behaviourFeatures = []string{
	// With --schema, a provider attribute a change adds or removes gets
	// the null side base_absent or head_absent marks
	// (internal/behaviour TestAbsentIsNull).
	"behaviour.null-sides",
	// A count or for_each change of a resource, data source or module
	// selects L2 next to L1
	// (internal/behaviour TestL2Selection).
	"behaviour.l2-count-for-each",
	// A referenced input, and a trigger at var.<name>, carry the variable's
	// declaration: added, default and nullable
	// (internal/behaviour TestDeclarationOracle).
	"behaviour.declarations",
	// With --schema, a resource or data source argument or block trigger
	// carries schema_required
	// (internal/behaviour TestSchemaRequired).
	"behaviour.schema-required",
	// The lint report carries in_repo_downgrade, the module areas whose
	// listed candidates the in-repo profile downgrades to MEDIUM
	// (internal/facts TestModuleCallersMonorepo, TestInRepoDowngradeConditions).
	"lint.in-repo-downgrade",
	// In a lint run with no change, a rule that needs the base is
	// not_applicable, not not_evaluated(no_base)
	// (internal/facts TestNeedsBase).
	"lint.no-change-not-applicable",
	// depth reads the module_root and unresolved fields of an affected
	// report: an example the report leaves out outside its module root
	// counts as reason all, an unresolved one is selected, and a report
	// whose module_root is unsafe is refused
	// (internal/depth TestFixtures module-root-*).
	"depth.module-root",
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "version",
		Short:   versionShort,
		Long:    versionLong,
		GroupID: groupOther,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !asJSON {
				fmt.Fprintln(cmd.OutOrStdout(), version())
				return nil
			}
			b, err := json.Marshal(newVersionReport(cmd.Root()))
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts: version:", err)
				return exitStatus{exitError}
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, versionJSONUsage)
	return cmd
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// newVersionReport builds the version --json report of the tree under root.
func newVersionReport(root *cobra.Command) versionReport {
	r := versionReport{
		SchemaVersion: versionSchemaVersion,
		Name:          "mtf-facts",
		Version:       version(),
		Go:            runtime.Version(),
		Reports:       reportVersions(),
		Features:      features(root),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		r.Go = bi.GoVersion
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				r.Commit = &s.Value
			case "vcs.modified":
				m := s.Value == "true"
				r.Modified = &m
			}
		}
	}
	if r.Commit == nil {
		r.Modified = nil
	}
	return r
}

// features lists every feature name of the tree under root, sorted:
//
//   - cmd:<path> for each command, the path's names joined by dots, such as
//     cmd:merge.check;
//   - flag:<path>:<name> for each long flag the command accepts, such as
//     flag:fixture-check:expect;
//   - <path>.<name> for a behaviour or an accepted value, such as
//     fixture-check.level.L2.
//
// Commands and flags are read from the tree, so they cannot drift from it.
// Help, completion, the root's own flags and hidden or deprecated entries
// are left out: they are no contract.
func features(root *cobra.Command) []string {
	out := commandFeatures(root)
	for _, l := range fixture.Levels() {
		out = append(out, "fixture-check.level."+l)
	}
	out = append(out, behaviourFeatures...)
	slices.Sort(out)
	return slices.Compact(out)
}

// commandFeatures lists the cmd: and flag: names of the tree under root,
// in walk order.
func commandFeatures(root *cobra.Command) []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Deprecated != "" || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			path := commandPath(sub)
			out = append(out, "cmd:"+path)
			add := func(f *pflag.Flag) {
				if !f.Hidden && f.Deprecated == "" && f.Name != "help" {
					out = append(out, "flag:"+path+":"+f.Name)
				}
			}
			sub.LocalFlags().VisitAll(add)
			sub.InheritedFlags().VisitAll(add)
			walk(sub)
		}
	}
	walk(root)
	return out
}

// commandPath is the dotted path of c below the root, such as merge.check.
func commandPath(c *cobra.Command) string {
	var names []string
	for ; c.HasParent(); c = c.Parent() {
		names = append(names, c.Name())
	}
	slices.Reverse(names)
	return strings.Join(names, ".")
}
