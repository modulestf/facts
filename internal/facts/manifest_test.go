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
		{Path: "modules/old", Kind: AreaSubmodule, Head: &Module{}, Base: &Module{Unsupported: []string{"x.tf.json is a JSON configuration file"}}},
	}
	m, fs, cs := Evaluate(areas, []Rule{finding, path})
	want := []ManifestEntry{
		{RuleID: "test.finding", Area: ".", Status: StatusFinding},
		{RuleID: "test.finding", Area: "modules/bad", Status: StatusNotEvaluated, Reason: "main.tf: parse error"},
		{RuleID: "test.finding", Area: "modules/old", Status: StatusNotEvaluated, Reason: "at base: x.tf.json is a JSON configuration file"},
		{RuleID: "test.path", Area: ".", Status: StatusNotApplicable},
		{RuleID: "test.path", Area: "modules/bad", Status: StatusClean},
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

// crossRule is a cross-area rule for tests: it finds in modules/a and returns
// a candidate in examples/x when its kinds include examples.
func crossRule(kinds ...string) Rule {
	line := 2
	return Rule{ID: "test.cross", Tier: "tool", Class: ClassConfig, Kinds: kinds, CrossArea: true,
		Check: func(a *Area) Result {
			switch a.Path {
			case "modules/a":
				return Result{Findings: []Finding{{RuleID: "test.cross", Severity: "HIGH", File: "modules/a/main.tf", Line: &line}}}
			case "examples/x":
				return Result{Candidates: []Candidate{{RuleID: "test.cross", File: "examples/x/main.tf", Line: &line}}}
			}
			return Result{}
		}}
}

func TestEvaluateCrossArea(t *testing.T) {
	root := &Area{Path: ".", Kind: AreaRoot, Head: &Module{}}
	modA := &Area{Path: "modules/a", Kind: AreaSubmodule, Head: &Module{}}
	modBad := &Area{Path: "modules/bad", Kind: AreaSubmodule, Head: &Module{}}
	modBad.Head.unsupported(ReasonParseError, "main.tf: parse error")
	modLink := &Area{Path: "modules/link", Kind: AreaSubmodule, Head: &Module{},
		Issue: "modules/link is a symbolic link, not a directory", issueCode: ReasonSymlink}
	exX := &Area{Path: "examples/x", Kind: AreaExample, Head: &Module{}}
	for name, tc := range map[string]struct {
		rule        Rule
		areas       []*Area
		want        ManifestEntry
		nFindings   int
		nCandidates int
	}{
		"clean, no area of its kinds": {
			rule: crossRule(AreaExample), areas: []*Area{root, modA},
			want: ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusClean},
		},
		"finding in a submodule": {
			rule: crossRule(AreaRoot, AreaSubmodule), areas: []*Area{root, modA, exX},
			want:      ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusFinding},
			nFindings: 1,
		},
		"candidate in an example, root not in scope": {
			rule: crossRule(AreaExample), areas: []*Area{exX},
			want:        ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusCandidate},
			nCandidates: 1,
		},
		"parse error in one area drops every finding": {
			rule: crossRule(AreaRoot, AreaSubmodule), areas: []*Area{root, modA, modBad},
			want: ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusNotEvaluated, Reason: ReasonParseError},
		},
		"unreadable area drops every finding": {
			rule: crossRule(AreaRoot, AreaSubmodule), areas: []*Area{root, modA, modLink},
			want: ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusNotEvaluated, Reason: ReasonSymlink},
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, fs, cs := Evaluate(tc.areas, []Rule{tc.rule})
			if !reflect.DeepEqual(m, []ManifestEntry{tc.want}) {
				t.Errorf("manifest\n got %+v\nwant %+v", m, []ManifestEntry{tc.want})
			}
			if len(fs) != tc.nFindings || len(cs) != tc.nCandidates {
				t.Errorf("findings %+v candidates %+v", fs, cs)
			}
			for _, f := range fs {
				if f.File != "modules/a/main.tf" {
					t.Errorf("finding lost its own file: %+v", f)
				}
			}
			if err := CheckManifest(m, []Rule{tc.rule}, tc.areas); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestCheckManifestCrossArea(t *testing.T) {
	areas := []*Area{{Path: ".", Kind: AreaRoot, Head: &Module{}}, {Path: "modules/a", Kind: AreaSubmodule, Head: &Module{}}}
	rules := []Rule{crossRule(AreaRoot, AreaSubmodule)}
	for name, m := range map[string][]ManifestEntry{
		"entry per area":  {{RuleID: "test.cross", Area: ".", Status: StatusClean}, {RuleID: "test.cross", Area: "modules/a", Status: StatusFinding}},
		"no root entry":   {{RuleID: "test.cross", Area: "modules/a", Status: StatusFinding}},
		"no entry":        {},
		"duplicate entry": {{RuleID: "test.cross", Area: ".", Status: StatusClean}, {RuleID: "test.cross", Area: ".", Status: StatusClean}},
	} {
		if err := CheckManifest(m, rules, areas); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
}

// A cross-area pass on a submodule-only change reads every area of the
// module, the untouched root included, and keeps only what it finds in the
// change. An untouched area it cannot read makes the entry not_evaluated.
func TestCrossAreaPassReadsUntouchedRoot(t *testing.T) {
	const sub = "variable \"x\" {}\n"
	for name, tc := range map[string]struct {
		baseRoot, headRoot string
		want               ManifestEntry
		wantFindings       []string
	}{
		"untouched root is read, its finding dropped": {
			baseRoot: "", headRoot: "",
			want:         ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusFinding},
			wantFindings: []string{"modules/a/variables.tf"},
		},
		"untouched root that cannot be parsed": {
			baseRoot: "variable {", headRoot: "variable {",
			want: ManifestEntry{RuleID: "test.cross", Area: ".", Status: StatusNotEvaluated, Reason: ReasonParseError},
		},
	} {
		t.Run(name, func(t *testing.T) {
			base := mustTree(t, writeTree(t, map[string]string{"main.tf": tc.baseRoot, "modules/a/variables.tf": ""}))
			head := mustTree(t, writeTree(t, map[string]string{"main.tf": tc.headRoot, "modules/a/variables.tf": sub}))
			areas, err := FindAreas(head, base, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			var read []string
			line := 1
			rule := Rule{ID: "test.cross", Tier: "tool", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule}, CrossArea: true,
				Check: func(a *Area) Result {
					read = append(read, a.Path)
					// A finding in every area it reads, so a kept root finding shows.
					return Result{Findings: []Finding{finding("test.cross", a.Path, "variables.tf", line, "s", "f")}}
				}}
			m, fs, cs := Evaluate(areas, []Rule{rule})
			if !reflect.DeepEqual(m, []ManifestEntry{tc.want}) {
				t.Errorf("manifest\n got %+v\nwant %+v", m, []ManifestEntry{tc.want})
			}
			if err := CheckManifest(m, []Rule{rule}, areas); err != nil {
				t.Error(err)
			}
			if tc.want.Status != StatusNotEvaluated && !reflect.DeepEqual(read, []string{".", "modules/a"}) {
				t.Errorf("pass read %v, want the root and modules/a", read)
			}
			var files []string
			for _, f := range fs {
				files = append(files, f.File)
			}
			if !reflect.DeepEqual(files, tc.wantFindings) || len(cs) != 0 {
				t.Errorf("findings %v candidates %+v, want %v", files, cs, tc.wantFindings)
			}
		})
	}
}
