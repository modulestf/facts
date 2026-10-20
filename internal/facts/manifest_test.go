package facts

import (
	"reflect"
	"testing"
)

func TestEvaluateStatuses(t *testing.T) {
	line := 3
	finding := Rule{ID: "test.finding", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule},
		Check: func(a *Area) Result {
			if a.Path == "." {
				return Result{Findings: []Finding{{RuleID: "test.finding", Severity: "LOW", File: "main.tf", Line: &line}}}
			}
			return Result{}
		}}
	path := Rule{ID: "test.path", Class: ClassPath, Kinds: []string{AreaSubmodule},
		Check: func(a *Area) Result {
			if a.Path == "modules/old" {
				return Result{Candidates: []Candidate{{RuleID: "test.path", File: "modules/old/x.tf"}}}
			}
			return Result{}
		}}
	areas := []*Area{
		{Path: ".", Kind: AreaRoot, Head: &Module{}},
		{Path: "modules/bad", Kind: AreaSubmodule, Head: &Module{Unsupported: []string{"main.tf: parse error"}}},
		{Path: "examples", Kind: AreaUnknown, Head: &Module{}},
		{Path: "modules/old", Kind: AreaSubmodule, Head: &Module{}, Base: &Module{Unsupported: []string{"x.tf.json is a JSON configuration file"}}},
	}
	m, fs, cs := Evaluate(areas, []Rule{finding, path})
	want := []ManifestEntry{
		{RuleID: "test.finding", Area: ".", Status: StatusFinding},
		{RuleID: "test.finding", Area: "modules/bad", Status: StatusNotEvaluated, Reason: "main.tf: parse error"},
		{RuleID: "test.finding", Area: "examples", Status: StatusNotEvaluated, Reason: "the area kind is unknown"},
		{RuleID: "test.finding", Area: "modules/old", Status: StatusNotEvaluated, Reason: "at base: x.tf.json is a JSON configuration file"},
		{RuleID: "test.path", Area: ".", Status: StatusNotApplicable},
		{RuleID: "test.path", Area: "modules/bad", Status: StatusClean},
		{RuleID: "test.path", Area: "examples", Status: StatusNotEvaluated, Reason: "the area kind is unknown"},
		{RuleID: "test.path", Area: "modules/old", Status: StatusCandidate},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("manifest\n got %+v\nwant %+v", m, want)
	}
	if len(fs) != 1 || fs[0].ID != "F-001" {
		t.Errorf("findings %+v", fs)
	}
	if len(cs) != 1 || cs[0].CandidateID != "C-001" {
		t.Errorf("candidates %+v", cs)
	}
}

func TestSortFindings(t *testing.T) {
	one, two := 1, 2
	fs := []Finding{
		{Severity: "LOW", File: "a.tf", Line: &one},
		{Severity: "MEDIUM", File: "b.tf", Line: nil},
		{Severity: "MEDIUM", File: "b.tf", Line: &two},
		{Severity: "MEDIUM", File: "a.tf", Line: &two},
		{Severity: "CRITICAL", File: "z.tf", Line: &one},
	}
	sortFindings(fs)
	var got []string
	for _, f := range fs {
		got = append(got, f.ID+" "+f.Severity+" "+f.File)
	}
	want := []string{"F-001 CRITICAL z.tf", "F-002 MEDIUM a.tf", "F-003 MEDIUM b.tf", "F-004 MEDIUM b.tf", "F-005 LOW a.tf"}
	if !reflect.DeepEqual(got, want) || fs[3].Line != nil {
		t.Errorf("got %v", got)
	}
}
