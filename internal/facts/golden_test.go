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

// addedRule stands for a rule a family file adds: it reports a HIGH finding
// in every area of every kind, so it sorts ahead of every MEDIUM finding and
// shifts their positional ids. id sets where it sorts among the other rules.
func addedRule(id string, crossArea bool) Rule {
	return Rule{
		ID: id, Tier: "tool", Class: ClassPath, Scope: ScopeAnyFileInArea, CrossArea: crossArea,
		Kinds: []string{AreaRoot, AreaSubmodule, AreaExample},
		Check: func(a *Area) Result {
			return Result{Findings: []Finding{{RuleID: id, Severity: "HIGH", File: a.Path, Summary: "s", SuggestedFix: "f"}}}
		},
	}
}

// goldenView is what expected.json holds.
type goldenView struct {
	Manifest   []ManifestEntry `json:"manifest"`
	Findings   []Finding       `json:"findings"`
	Candidates []Candidate     `json:"candidates"`
}

// listedOnly keeps the entries, findings and candidates of the listed rules,
// in their order, with the F- and C- ids cleared.
func listedOnly(v goldenView, listed []Rule) goldenView {
	keep := func(id string) bool { return slices.ContainsFunc(listed, func(r Rule) bool { return r.ID == id }) }
	out := goldenView{Manifest: []ManifestEntry{}, Findings: []Finding{}, Candidates: []Candidate{}}
	for _, e := range v.Manifest {
		if keep(e.RuleID) {
			out.Manifest = append(out.Manifest, e)
		}
	}
	for _, f := range v.Findings {
		if keep(f.RuleID) {
			f.ID = ""
			out.Findings = append(out.Findings, f)
		}
	}
	for _, c := range v.Candidates {
		if keep(c.RuleID) {
			c.CandidateID = ""
			out.Candidates = append(out.Candidates, c)
		}
	}
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestAddedRuleChangesNoOtherCase joins a new rule into the registry, as a
// family file would, runs every case with the full rule set, as Run does,
// and checks that the results of the rules each case lists still match its
// expected.json. The new rule runs alongside them, per area and as a
// cross-area pass, and its HIGH findings sort ahead of theirs. Its id sorts
// once after every rule and once before every rule, so a rule that mutates a
// shared Area breaks a rule evaluated after it in one of the two.
//
// Finding and candidate ids are positional: F-NNN and C-NNN number the whole
// report in sort order, so a rule that adds findings renumbers the ids of
// other rules in the same run. That is how the report is defined, so this
// comparison ignores the ids; the golden files pin them only for the rules a
// case lists.
func TestAddedRuleChangesNoOtherCase(t *testing.T) {
	cases := goldenCases(t)
	for _, v := range []struct {
		id    string
		cross bool
	}{
		{"structure.zz-added-for-test", false},
		{"structure.zz-added-for-test", true},
		{"aaa.added-for-test", false},
		{"aaa.added-for-test", true},
	} {
		added := addedRule(v.id, v.cross)
		rules := joinRules(Rules, []Rule{added})
		for _, dir := range cases {
			listed := caseRules(t, dir, Rules)
			var want goldenView
			if err := json.Unmarshal(readExpected(t, dir), &want); err != nil {
				t.Fatal(err)
			}
			var full goldenView
			if err := json.Unmarshal(goldenOutput(t, dir, rules), &full); err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(full.Manifest, func(e ManifestEntry) bool { return e.RuleID == added.ID }) {
				t.Fatalf("%s: the added rule did not run", filepath.Base(dir))
			}
			if !bytes.Equal(mustJSON(t, listedOnly(full, listed)), mustJSON(t, listedOnly(want, listed))) {
				t.Errorf("%s cross-area %v: %s changed after a rule it does not list was added", v.id, v.cross, filepath.Base(dir))
			}
		}
	}
	// Control: a comparison that keeps the added rule does see it.
	listed := append(caseRules(t, cases[0], Rules), addedRule("structure.zz-added-for-test", false))
	if bytes.Equal(goldenOutput(t, cases[0], listed), readExpected(t, cases[0])) {
		t.Errorf("%s did not change with the added rule listed", filepath.Base(cases[0]))
	}
}

// TestHookInputsChangeOnlyTheEnvelope sets a schema handoff and the test
// profile on every case. No rule reads either yet, so the manifest, findings
// and candidates stay the same; only the envelope records the two inputs.
func TestHookInputsChangeOnlyTheEnvelope(t *testing.T) {
	for _, dir := range goldenCases(t) {
		o := Options{Head: filepath.Join(dir, "head"), Limits: DefaultLimits,
			SchemaHandoff: "../../schema/examples/schema-handoff.valid.json", ProfileDir: testProfile}
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
		if with.Envelope.SchemaInputs == nil {
			t.Errorf("%s: no schema_inputs", filepath.Base(dir))
		}
		if with.Envelope.ProfileSHA256 == nil {
			t.Errorf("%s: no profile_sha256", filepath.Base(dir))
		}
		with.Envelope.SchemaInputs, with.Envelope.ProfileSHA256 = nil, nil
		if !bytes.Equal(mustJSON(t, without), mustJSON(t, with)) {
			t.Errorf("%s: schema or profile inputs changed more than the envelope", filepath.Base(dir))
		}
	}
}
