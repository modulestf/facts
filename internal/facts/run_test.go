package facts

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const understatedRule = "structure.version-floor-understated"

// crossArea reports whether a rule is cross-area: its "." entry is there
// whatever the change touches.
func crossArea(id string) bool {
	return slices.ContainsFunc(Rules, func(r Rule) bool { return r.ID == id && r.CrossArea })
}

func runDiffFixture(t *testing.T, o Options) *Report {
	t.Helper()
	o.Head, o.Limits = filepath.Join(diffFixture, "head"), DefaultLimits
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRunDiffMatchesBase checks that a run with the change as a diff, in
// either format, reports what a run with the real base tree does.
func TestRunDiffMatchesBase(t *testing.T) {
	want := runDiffFixture(t, Options{Base: filepath.Join(diffFixture, "base")})
	if want.Scope.Change != ChangeBase {
		t.Fatalf("scope %+v", want.Scope)
	}
	for _, f := range []string{"files.json", "change.diff"} {
		t.Run(f, func(t *testing.T) {
			got := runDiffFixture(t, Options{Diff: filepath.Join(diffFixture, f)})
			if got.Scope.Change != ChangeDiff || got.Envelope.Inputs.Diff == nil {
				t.Errorf("scope %+v, diff input %+v", got.Scope, got.Envelope.Inputs.Diff)
			}
			if got.Envelope.Inputs.Base.TreeSHA256 != want.Envelope.Inputs.Base.TreeSHA256 {
				t.Error("rebuilt base tree hash differs")
			}
			got.Scope.Change = ChangeBase
			for name, pair := range map[string][2]any{
				"manifest":   {got.Manifest, want.Manifest},
				"findings":   {got.Findings, want.Findings},
				"candidates": {got.Candidates, want.Candidates},
				"callers":    {got.Callers, want.Callers},
				"scope":      {got.Scope, want.Scope},
			} {
				if !reflect.DeepEqual(pair[0], pair[1]) {
					g, _ := json.Marshal(pair[0])
					w, _ := json.Marshal(pair[1])
					t.Errorf("%s differs\n got %s\nwant %s", name, g, w)
				}
			}
		})
	}
}

// TestRunDiffEvaluatesCompat checks that the base-dependent rules evaluate
// with a diff, where without a change they are no_base.
func TestRunDiffEvaluatesCompat(t *testing.T) {
	noChange := runDiffFixture(t, Options{})
	diff := runDiffFixture(t, Options{Diff: filepath.Join(diffFixture, "files.json")})
	for _, tc := range []struct {
		r    *Report
		want bool // every compat entry is no_base
	}{{noChange, true}, {diff, false}} {
		n := 0
		for _, e := range tc.r.Manifest {
			if strings.HasPrefix(e.RuleID, "compat.") && e.Status != StatusNotApplicable {
				n++
				if (e.ReasonCode == ReasonNoBase) != tc.want {
					t.Errorf("%s in %s: %s %s", e.RuleID, e.Area, e.Status, e.ReasonCode)
				}
			}
		}
		if n == 0 {
			t.Error("no compat entry")
		}
	}
}

// TestRunDiffNoPatch checks a file the diff gives no patch for leaves its
// area's configuration rules not_evaluated(no_base), saying why.
func TestRunDiffNoPatch(t *testing.T) {
	head := writeTree(t, map[string]string{"modules/m/main.tf": "resource \"aws_demo_widget\" \"this\" {}\n", "main.tf": "locals {\n  a = 1\n}\n"})
	diff := filepath.Join(writeTree(t, map[string]string{"files.json": `[{"filename":"modules/m/main.tf","status":"modified","additions":9000,"deletions":1}]`}), "files.json")
	r, err := Run(Options{Head: head, Diff: diff, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range r.Manifest {
		if e.Area == "." && !crossArea(e.RuleID) {
			t.Errorf("untouched root in scope: %+v", e)
		}
		if e.RuleID == "compat.variable-removed" && e.Area == "modules/m" {
			found = true
			if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonNoBase || !strings.Contains(e.Reason, "no patch") {
				t.Errorf("entry %+v", e)
			}
		}
	}
	if !found {
		t.Error("no compat.variable-removed entry in modules/m")
	}
	if _, err := Run(Options{Head: head, Diff: diff, Base: head, Limits: DefaultLimits}); err == nil {
		t.Error("--base with --diff: no error")
	}
}

// TestRunScope checks which areas a change checks, which it reaches only as
// callers of a changed module, and which it leaves out; --full-tree checks
// every area; without a change every area is checked.
func TestRunScope(t *testing.T) {
	all := []string{".", "examples/a", "examples/b", "examples/c", "examples/old", "modules/log", "modules/other"}
	for _, tc := range []struct {
		name string
		o    Options
		want Scope
	}{
		{"change", Options{Diff: filepath.Join(diffFixture, "files.json")}, Scope{Change: ChangeDiff,
			AreasChecked: []string{"examples/c", "examples/old", "modules/log"}, AreasCalling: []string{".", "examples/a", "examples/b"}, AreasLeftOut: []string{"modules/other"}}},
		{"full tree", Options{Diff: filepath.Join(diffFixture, "files.json"), FullTree: true}, Scope{Change: ChangeDiff, FullTree: true,
			AreasChecked: all, AreasCalling: []string{}, AreasLeftOut: []string{}}},
		{"no change", Options{}, Scope{Change: ChangeNone,
			AreasChecked: slices.DeleteFunc(slices.Clone(all), func(a string) bool { return a == "examples/old" }), AreasCalling: []string{}, AreasLeftOut: []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runDiffFixture(t, tc.o)
			if !reflect.DeepEqual(*r.Scope, tc.want) {
				t.Errorf("scope\n got %+v\nwant %+v", *r.Scope, tc.want)
			}
			var areas []string
			for _, e := range r.Manifest {
				if !slices.Contains(areas, e.Area) && !crossArea(e.RuleID) {
					areas = append(areas, e.Area)
				}
			}
			slices.Sort(areas)
			if !slices.Equal(areas, tc.want.AreasChecked) {
				t.Errorf("manifest areas %v, want %v", areas, tc.want.AreasChecked)
			}
		})
	}
}

// TestRunCrossAreaFiling checks the version floor candidates of the callers
// of a raised module land on each caller's own floor, and the manifest files
// each under its caller's area, the root's under ".".
func TestRunCrossAreaFiling(t *testing.T) {
	r := runDiffFixture(t, Options{Diff: filepath.Join(diffFixture, "files.json")})
	var got []string
	for _, c := range r.Candidates {
		if c.RuleID == understatedRule {
			got = append(got, c.File+":"+itoa(*c.Line)+" "+c.Subject)
		}
	}
	want := []string{"examples/a/versions.tf:7 examples/a:required_providers.aws", "versions.tf:7 .:required_providers.aws"}
	if !slices.Equal(got, want) {
		t.Errorf("candidates\n got %v\nwant %v", got, want)
	}
	var entries []string
	for _, e := range r.Manifest {
		if e.RuleID == understatedRule {
			entries = append(entries, e.Area+" "+e.Status)
		}
	}
	if want := []string{". candidate", "examples/a candidate"}; !slices.Equal(entries, want) {
		t.Errorf("manifest %v, want %v", entries, want)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// TestRunCallers checks the callers section: the changed module with its
// floors at base and head, and each caller with its own floors and the floor
// candidates filed in its area.
func TestRunCallers(t *testing.T) {
	r := runDiffFixture(t, Options{Diff: filepath.Join(diffFixture, "files.json")})
	floors := func(aws string) []Floor {
		return []Floor{
			{Address: "required_providers.aws", Source: new("registry.terraform.io/hashicorp/aws"), Constraint: new(">= " + aws)},
			{Address: "required_version", Constraint: new(">= 1.5.7")},
		}
	}
	cand := func(subject string) []string {
		for _, c := range r.Candidates {
			if c.RuleID == understatedRule && c.Subject == subject {
				return []string{c.CandidateID}
			}
		}
		return []string{}
	}
	want := []ChangedModule{{
		Module: "modules/log", Changed: []string{"inputs", "versions"}, BaseFloors: floors("5.81"), HeadFloors: floors("6.25"),
		Callers: []Caller{
			{Area: ".", File: "main.tf", Line: 2, Floors: floors("5.81"), Candidates: cand(".:required_providers.aws")},
			{Area: "examples/a", File: "examples/a/main.tf", Line: 2, Floors: floors("5.81"), Candidates: cand("examples/a:required_providers.aws")},
			{Area: "examples/b", File: "examples/b/main.tf", Line: 2, Floors: floors("6.30"), Candidates: []string{}},
			{Area: "examples/c", File: "examples/c/main.tf", Line: 2, Checked: true, Floors: floors("6.25"), Candidates: []string{}},
		},
	}}
	if r.Callers == nil || !reflect.DeepEqual(*r.Callers, want) {
		g, _ := json.MarshalIndent(r.Callers, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("callers\n got %s\nwant %s", g, w)
	}
	if r := runDiffFixture(t, Options{}); r.Callers != nil {
		t.Errorf("callers without a change: %+v", r.Callers)
	}
}
