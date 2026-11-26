package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/depth"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schematest"
	"github.com/spf13/cobra"
)

// TestReportsMatchSchemas runs each command whose JSON output this package
// writes and validates the real output against the command's schema. The
// other reports are checked where they are built: lint, affected and
// schema-diff in internal/facts, bundle and merge final in their tests.
func TestReportsMatchSchemas(t *testing.T) {
	set := schematest.Load(t, "../../schema")
	merge := filepath.Join(mergeFixtures, "counts-from-repair")
	dup := filepath.Join(mergeFixtures, "answered-once-duplicate")
	invalidProfile := relaxedProfile(t)
	doc, err := os.ReadFile(filepath.Join(invalidProfile, "PROFILE.md"))
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(doc), "release: none", "release: maybe", 1)
	if err := os.WriteFile(filepath.Join(invalidProfile, "PROFILE.md"), []byte(bad), 0o644); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	const diffBase = "../../testdata/diff-base"
	statsFile := filepath.Join(t.TempDir(), "stats.json")
	for _, tc := range []struct {
		name, schema string
		args         []string
		code         int
	}{
		{"changes", "changes.schema.json", []string{"changes", changesHeredoc + "/head", "--base", changesHeredoc + "/base", "--json"}, exitClean},
		{"lint stats", "stats.schema.json", []string{"lint", "../../testdata/variable-untyped-base/head", "--json", "--stats-file", statsFile}, exitFindings},
		{"depth", "depth.schema.json", []string{"depth", "--head", diffBase + "/head", "--base", diffBase + "/base", "--files", diffBase + "/files.json"}, exitClean},
		{"depth odd example names", "depth.schema.json", []string{"depth", "--head", diffBase + "/head", "--base", diffBase + "/base", "--files", diffBase + "/files.json", "--examples", "examples/a b,examples/v1+x,examples/.hidden,examples/..."}, exitClean},
		{"ledger inventory", "ledger-report.schema.json", []string{"ledger", ledgerModule, "--schema-files", ledgerSchema}, exitClean},
		{"ledger check", "ledger-report.schema.json", []string{"ledger", ledgerModule, "--schema-files", ledgerSchema, "--check", "../../testdata/ledger/valid.json"}, exitClean},
		{"ledger problems", "ledger-report.schema.json", []string{"ledger", ledgerModule, "--schema-files", ledgerSchema, "--check", "../../testdata/ledger/unmapped.json"}, exitFindings},
		{"profile-check", "profile-check.schema.json", []string{"profile-check", "--profile-dir", cliTestProfile}, exitClean},
		{"profile-check gates", "profile-check.schema.json", []string{"profile-check", "--profile-dir", relaxedProfile(t), "--gates", "--downgrade-ids"}, exitClean},
		{"profile-check invalid", "profile-check.schema.json", []string{"profile-check", "--profile-dir", invalidProfile, "--gates"}, exitProfileInvalid},
		{"profile-snapshot", "profile-snapshot.schema.json", []string{"profile-snapshot", "--profile-dir", cliTestProfile}, exitClean},
		{"merge check valid", "merge-check.schema.json", []string{"merge", "check", regionFile(t, "answered-once-duplicate"), filepath.Join(dup, "model-output.repair.json")}, exitReplyValid},
		{"merge check repair", "merge-check.schema.json", []string{"merge", "check", regionFile(t, "answered-once-duplicate"), filepath.Join(dup, "model-output.json")}, exitReplyRepair},
		{"merge counts", "merge-counts.schema.json", []string{"merge", "counts", regionFile(t, "counts-from-repair"), filepath.Join(merge, "model-output.json"), filepath.Join(merge, "model-output.repair.json")}, exitClean},
		{"merge counts null", "merge-counts.schema.json", []string{"merge", "counts", regionFile(t, "counts-from-repair"), filepath.Join(merge, "model-output.json"), filepath.Join(dup, "model-output.repair.json")}, exitClean},
		{"rules", "rules.schema.json", []string{"rules", "--json"}, exitClean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runCLI(tc.args)
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, errOut)
			}
			if tc.name == "lint stats" {
				b, err := os.ReadFile(statsFile)
				if err != nil {
					t.Fatal(err)
				}
				out = string(b)
			}
			if tc.name == "profile-snapshot" {
				var s snapOut
				if err := json.Unmarshal([]byte(out), &s); err == nil && s.Dir != "" {
					t.Cleanup(func() { _ = os.RemoveAll(s.Dir) })
				}
			}
			if errs := set.Validate(tc.schema, schematest.DecodeJSON(t, []byte(out))); len(errs) > 0 {
				t.Errorf("%s: %v\n%s", tc.schema, errs, out)
			}
		})
	}
}

// TestDepthSchemaVersion checks that the depth report carries schema_version
// next to format, both at the current version.
func TestDepthSchemaVersion(t *testing.T) {
	const dir = "../../testdata/diff-base"
	code, out, errOut := runCLI([]string{"depth", "--head", dir + "/head", "--base", dir + "/base", "--files", dir + "/files.json"})
	if code != exitClean {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var r struct {
		Format        *int `json:"format"`
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Format == nil || *r.Format != depth.Format || r.SchemaVersion == nil || *r.SchemaVersion != depth.SchemaVersion {
		t.Errorf("format %v, schema_version %v", r.Format, r.SchemaVersion)
	}
}

// TestRulesJSON checks rules --json against facts.Rules: every rule in id
// order, area kinds sorted, the flags as the Rule struct holds them.
func TestRulesJSON(t *testing.T) {
	code, out, errOut := runCLI([]string{"rules", "--json"})
	if code != exitClean || errOut != "" {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		SchemaVersion int        `json:"schema_version"`
		Rules         []ruleJSON `json:"rules"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != rulesSchemaVersion || len(got.Rules) != len(facts.Rules) {
		t.Fatalf("schema_version %d, %d rules, want %d", got.SchemaVersion, len(got.Rules), len(facts.Rules))
	}
	for i, r := range facts.Rules {
		g := got.Rules[i]
		want := ruleJSON{ID: r.ID, Tier: r.Tier, Class: r.Class, Scope: r.Scope, AreaKinds: slices.Sorted(slices.Values(r.Kinds)),
			CrossArea: r.CrossArea, NeedsBase: r.NeedsBase, ReadsUnreadConfig: r.ReadsUnreadConfig}
		if g.ID != want.ID || g.Tier != want.Tier || g.Class != want.Class || g.Scope != want.Scope || !slices.Equal(g.AreaKinds, want.AreaKinds) ||
			g.CrossArea != want.CrossArea || g.NeedsBase != want.NeedsBase || g.ReadsUnreadConfig != want.ReadsUnreadConfig {
			t.Errorf("rule %d: got %+v, want %+v", i, g, want)
		}
	}
	// Every key is written, the false flags too, so a consumer never has to
	// tell a missing key from false.
	if strings.Count(out, `"reads_unread_config"`) != len(facts.Rules) {
		t.Error("a rule lacks reads_unread_config")
	}
	// The text form is unchanged without the flag.
	if _, text, _ := runCLI([]string{"rules"}); strings.HasPrefix(text, "{") {
		t.Errorf("rules without --json printed JSON: %q", text)
	}
}

// reportSchemas maps each command that writes JSON, by its path under the
// root, to the schema of that JSON under schema/. A command with no JSON
// output is listed in noJSON.
var reportSchemas = map[string]string{
	"affected":         "affected.schema.json",
	"behaviour":        "behaviour.schema.json",
	"bundle":           "facts-bundle.schema.json",
	"changes":          "changes.schema.json",
	"claims-eval":      "claims-eval.schema.json",
	"depth":            "depth.schema.json",
	"fixture-check":    "fixture-check.schema.json",
	"ledger":           "ledger-report.schema.json",
	"lint":             "lint.schema.json",
	"merge check":      "merge-check.schema.json",
	"merge counts":     "merge-counts.schema.json",
	"merge final":      "merged-findings.schema.json",
	"profile-check":    "profile-check.schema.json",
	"profile-snapshot": "profile-snapshot.schema.json",
	"rules":            "rules.schema.json",
	"schema-diff":      "schema-diff.schema.json",
	"version":          "version.schema.json",
}

// noJSON are the commands with no JSON output.
var noJSON = []string{"completion", "help", "merge", "pre-commit", "pre-commit derive", "pre-commit docs", "pre-commit wrapper"}

// TestEveryJSONCommandHasSchema checks that each command of the CLI is
// either mapped to a schema with a valid example, which tests/schemas-test.sh
// checks, or listed as writing no JSON. A new command fails here until it
// is one or the other.
func TestEveryJSONCommandHasSchema(t *testing.T) {
	var paths []string
	var walk func(c *cobra.Command, prefix string)
	walk = func(c *cobra.Command, prefix string) {
		for _, sub := range c.Commands() {
			p := strings.TrimSpace(prefix + " " + sub.Name())
			paths = append(paths, p)
			walk(sub, p)
		}
	}
	root := newRootCmd(io.Discard, io.Discard)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	walk(root, "")
	for _, p := range paths {
		if strings.HasPrefix(p, "completion ") {
			continue
		}
		schema, ok := reportSchemas[p]
		if !ok {
			if !slices.Contains(noJSON, p) {
				t.Errorf("command %q is neither mapped to a schema nor listed as writing no JSON", p)
			} else if c, _, err := root.Find(strings.Fields(p)); err == nil && c.Flags().Lookup("json") != nil {
				t.Errorf("command %q has a --json flag but is listed as writing no JSON", p)
			}
			continue
		}
		name := strings.TrimSuffix(schema, ".schema.json")
		for _, f := range []string{schema, "examples/" + name + ".valid.json"} {
			if _, err := os.Stat(filepath.Join("../../schema", f)); err != nil {
				t.Errorf("command %q: %v", p, err)
			}
		}
	}
	for p := range reportSchemas {
		if !slices.Contains(paths, p) {
			t.Errorf("reportSchemas lists %q, which is no command", p)
		}
	}
}
