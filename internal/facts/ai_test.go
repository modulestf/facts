package facts

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestAI keeps the entries a reviewer decides on, in manifest order,
// counts the rest by status and rule family, and keeps the tree hashes and
// counts in place of the file lists.
func TestAI(t *testing.T) {
	sum := strings.Repeat("a", 64)
	r := &Report{
		Envelope: Envelope{SchemaVersion: SchemaVersion, Tool: Tool{Name: "mtf-facts", Version: "test", BinarySHA256: sum},
			RegistrySHA256: &sum, ProfileSHA256: &sum, Inputs: Inputs{
				Head: TreeInput{TreeSHA256: sum, Files: []FileInput{{Path: "main.tf", Kind: "file", SHA256: sum}, {Path: "x.tf", Kind: "symlink"}}},
				Base: &TreeInput{TreeSHA256: sum, Files: []FileInput{}},
				Diff: &DiffInput{Format: DiffFormatGitHubFiles, SHA256: sum},
			}},
		Manifest: []ManifestEntry{
			{RuleID: "compat.a", Area: ".", Status: StatusClean},
			{RuleID: "structure.b", Area: "modules/m", Status: StatusCandidate},
			{RuleID: "structure.c", Area: "examples/e", Status: StatusNotApplicable},
			{RuleID: "compat.a", Area: "modules/m", Status: StatusNotEvaluated, Reason: "r", ReasonCode: ReasonParseError},
			{RuleID: "structure.b", Area: ".", Status: StatusClean},
			{RuleID: "scope.d", Area: ".", Status: StatusFinding},
		},
		Findings:      []Finding{},
		Candidates:    []Candidate{},
		Index:         []IndexEntry{{File: "main.tf", StartLine: 1, EndLine: 1, Subject: ".:var.x"}},
		InterfaceDiff: &InterfaceDiff{},
		Scope:         &Scope{Change: ChangeDiff, AreasChecked: []string{"."}},
	}
	c := r.AIReport()
	if got := c.Manifest; !slices.Equal(got, []ManifestEntry{r.Manifest[1], r.Manifest[3], r.Manifest[5]}) {
		t.Errorf("manifest %+v", got)
	}
	want := ManifestSummary{
		Status:  StatusCounts{Clean: 2, NotApplicable: 1, Finding: 1, Candidate: 1, NotEvaluated: 1},
		Omitted: map[string]OmittedCounts{"compat": {Clean: 1}, "structure": {Clean: 1, NotApplicable: 1}},
		Areas:   []string{".", "examples/e", "modules/m"},
	}
	if g := c.ManifestSummary; g.Status != want.Status || !slices.Equal(g.Areas, want.Areas) || len(g.Omitted) != 2 ||
		g.Omitted["compat"] != want.Omitted["compat"] || g.Omitted["structure"] != want.Omitted["structure"] {
		t.Errorf("summary %+v", g)
	}
	in := c.Envelope.Inputs
	if in.Head != (AITree{TreeSHA256: sum, FileCount: 2}) || in.Base == nil || *in.Base != (AITree{TreeSHA256: sum}) || in.Diff != r.Envelope.Inputs.Diff {
		t.Errorf("inputs %+v", in)
	}
	if c.Envelope.RegistrySHA256 != &sum || c.Envelope.ProfileSHA256 != &sum || c.Scope != r.Scope {
		t.Errorf("envelope or scope not kept: %+v", c.Envelope)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"index", "interface_diff", "schema_coverage"} {
		if _, ok := keys[k]; ok {
			t.Errorf("AI report has %s", k)
		}
	}
	if strings.Contains(string(data), `"files"`) {
		t.Errorf("AI report lists files: %s", data)
	}
}

// TestAIEmpty writes empty lists, never null, for the keys a consumer
// requires.
func TestAIEmpty(t *testing.T) {
	r := &Report{Envelope: Envelope{Inputs: Inputs{Head: TreeInput{Files: []FileInput{}}}}, Findings: []Finding{}, Candidates: []Candidate{}}
	data, err := json.Marshal(r.AIReport())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"manifest":[]`, `"areas":[]`, `"omitted_by_family":{}`, `"base":null`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lacks %s: %s", want, data)
		}
	}
}
