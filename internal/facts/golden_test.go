package facts

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*/expected.json")

// goldenCases returns the case directories under testdata/.
func goldenCases(t *testing.T) []string {
	t.Helper()
	heads, err := filepath.Glob("../../testdata/*/head")
	if err != nil || len(heads) == 0 {
		t.Fatalf("no golden cases: %v", err)
	}
	dirs := make([]string, len(heads))
	for i, h := range heads {
		dirs[i] = filepath.Dir(h)
	}
	return dirs
}

// caseRules returns the rules of from that the case in dir lists in its
// rules file, one id per line. Every listed id must be in from, so a renamed
// or dropped rule fails its cases instead of leaving them empty.
func caseRules(t *testing.T, dir string, from []Rule) []Rule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "rules"))
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(data))
	if len(ids) == 0 {
		t.Fatalf("%s/rules lists no rule", dir)
	}
	var out []Rule
	for _, id := range ids {
		i := slices.IndexFunc(from, func(r Rule) bool { return r.ID == id })
		if i < 0 {
			t.Fatalf("%s/rules: unknown rule %q", dir, id)
		}
		out = append(out, from[i])
	}
	return out
}

// goldenOutput runs the case in dir with rules and renders what expected.json holds.
func goldenOutput(t *testing.T, dir string, rules []Rule) []byte {
	t.Helper()
	o := Options{Head: filepath.Join(dir, "head"), Limits: DefaultLimits}
	if _, err := os.Stat(filepath.Join(dir, "base")); err == nil {
		o.Base = filepath.Join(dir, "base")
	}
	r, err := run(o, rules)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(struct {
		Manifest   []ManifestEntry `json:"manifest"`
		Findings   []Finding       `json:"findings"`
		Candidates []Candidate     `json:"candidates"`
	}{r.Manifest, r.Findings, r.Candidates}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(got, '\n')
}

func readExpected(t *testing.T, dir string) []byte {
	t.Helper()
	want, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	return want
}

// TestGolden runs every case under testdata/. A case has head/, an optional
// base/, a rules file naming the rules it compares, and expected.json with
// the manifest, findings and candidates a person checked for those rules.
func TestGolden(t *testing.T) {
	for _, dir := range goldenCases(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			got := goldenOutput(t, dir, caseRules(t, dir, Rules))
			if *update {
				if err := os.WriteFile(filepath.Join(dir, "expected.json"), got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if want := readExpected(t, dir); !bytes.Equal(got, want) {
				t.Errorf("output differs from %s/expected.json:\n%s", dir, got)
			}
		})
	}
}

// addedRule stands for a rule a family file adds: it reports a finding in
// every area of every kind, so any case that evaluated it would change.
var addedRule = Rule{
	ID: "structure.zz-added-for-test", Tier: "tool", Class: ClassPath, Scope: ScopeAnyFileInArea,
	Kinds: []string{AreaRoot, AreaSubmodule, AreaExample},
	Check: func(a *Area) Result {
		return Result{Findings: []Finding{{RuleID: "structure.zz-added-for-test", Severity: "LOW", File: a.Path, Summary: "s", SuggestedFix: "f"}}}
	},
}

// TestAddedRuleChangesNoOtherCase joins a new rule into the registry, as a
// family file would, and checks that every case that does not list it keeps
// its expected output byte for byte. A case that does list it does change,
// so the check is not vacuous.
func TestAddedRuleChangesNoOtherCase(t *testing.T) {
	rules := joinRules(Rules, []Rule{addedRule})
	cases := goldenCases(t)
	for _, dir := range cases {
		if got := goldenOutput(t, dir, caseRules(t, dir, rules)); !bytes.Equal(got, readExpected(t, dir)) {
			t.Errorf("%s changed after a rule it does not list was added", filepath.Base(dir))
		}
	}
	listed := append(caseRules(t, cases[0], rules), addedRule)
	if bytes.Equal(goldenOutput(t, cases[0], listed), readExpected(t, cases[0])) {
		t.Errorf("%s did not change with the added rule listed", filepath.Base(cases[0]))
	}
}

// TestNoOpHooksChangeNoOutput sets the schema and profile inputs, which the
// no-op hooks ignore, and checks every case keeps its expected output.
func TestNoOpHooksChangeNoOutput(t *testing.T) {
	for _, dir := range goldenCases(t) {
		o := Options{Head: filepath.Join(dir, "head"), Limits: DefaultLimits,
			SchemaHandoff: "handoff.json", SchemaFiles: "schemas", ProfileDir: "profile"}
		if _, err := os.Stat(filepath.Join(dir, "base")); err == nil {
			o.Base = filepath.Join(dir, "base")
		}
		without, err := run(Options{Head: o.Head, Base: o.Base, Limits: DefaultLimits}, Rules)
		if err != nil {
			t.Fatal(err)
		}
		with, err := run(o, Rules)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(without)
		b, _ := json.Marshal(with)
		if !bytes.Equal(a, b) {
			t.Errorf("%s: schema and profile inputs changed the report", filepath.Base(dir))
		}
	}
}
