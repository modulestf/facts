package facts

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
)

// runCoverageReach runs head and base trees with the schemas of the
// coverage-reach-positive handoff, its tree hashes replaced by theirs so it
// binds.
func runCoverageReach(t *testing.T, head, base map[string]string) *Report {
	t.Helper()
	o := Options{Head: writeTree(t, head), Base: writeTree(t, base), HeadSHA: oracleHeadSHA, MergeBaseSHA: oracleBaseSHA, Limits: DefaultLimits}
	data, err := os.ReadFile("../../testdata/coverage-reach-positive/schema-handoff.json")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatal(err)
	}
	in := h["inputs"].(map[string]any)
	in["head"].(map[string]any)["tree_hash"] = tfTreeHash(mustTree(t, o.Head))
	in["merge_base"].(map[string]any)["tree_hash"] = tfTreeHash(mustTree(t, o.Base))
	o.SchemaHandoff = filepath.Join(t.TempDir(), "schema-handoff.json")
	if err := os.WriteFile(o.SchemaHandoff, mustJSON(t, h), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run(o, coverageReachRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A base value past the cap is cut and makes the evidence incomplete.
func TestCoverageReachEvidenceCap(t *testing.T) {
	long := `"` + strings.Repeat("x", 2*maxEvidenceText) + `"`
	block := func(name string) string {
		return "resource \"aws_demo_widget\" \"this\" {\n  name = " + name + "\n  listener {\n    port = 1\n  }\n}\n"
	}
	r := runCoverageReach(t, map[string]string{"main.tf": block("null")}, map[string]string{"main.tf": block(long)})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.RuleID != "coverage.required-argument-unreachable" || c.EvidenceComplete || len(*c.Evidence.Base) != maxEvidenceText || *c.Evidence.Head != "null" {
		t.Errorf("cap did not bite: %s complete=%v", c.RuleID, c.EvidenceComplete)
	}
}

// A block whose provider has no schema makes every rule not_evaluated, and
// an untouched one leaves them clean.
func TestCoverageReachSchemaUnavailable(t *testing.T) {
	block := "resource \"other_thing\" \"this\" {\n  name = 1\n}\n"
	for _, c := range []struct{ base, want string }{{"", StatusNotEvaluated}, {block, StatusClean}} {
		r := runCoverageReach(t, map[string]string{"main.tf": block, "variables.tf": ""}, map[string]string{"main.tf": c.base})
		for _, e := range r.Manifest {
			if e.Status != c.want || (c.want == StatusNotEvaluated) != (e.ReasonCode == ReasonSchemaUnavailable) {
				t.Errorf("base %q: %s is %s %s", c.base, e.RuleID, e.Status, e.ReasonCode)
			}
		}
	}
}

func TestOutputAttrs(t *testing.T) {
	r := Resource{Mode: "resource", Type: "aws_demo_widget", Name: "this"}
	for _, c := range []struct {
		value string
		read  []string
		whole bool
	}{
		{"aws_demo_widget.this.arn", []string{"arn"}, false},
		{"aws_demo_widget.this[0].arn", []string{"arn"}, false},
		{`try(aws_demo_widget.this["k"].arn, null)`, []string{"arn"}, false},
		{"aws_demo_widget.this[var.k].id", []string{"id"}, false},
		{"aws_demo_widget.this[*].arn", []string{"arn"}, false},
		{"aws_demo_widget.this.*.arn", []string{"arn"}, false},
		{"(aws_demo_widget.this)", nil, true},
		{"aws_demo_widget.this[0]", nil, true},
		{"try(aws_demo_widget.this[0], null)", nil, false},
		{"[for w in aws_demo_widget.this : w.arn]", nil, false},
		{"local.arn", nil, false},
		{"aws_demo_widget.other.arn", nil, false},
		{"data.aws_demo_widget.this.arn", nil, false},
	} {
		read := map[string]bool{}
		whole := outputAttrs(Expr{Text: c.value, Range: hcl.Range{Start: hcl.InitialPos}}, "outputs.tf", r, read)
		if got := slices.Sorted(maps.Keys(read)); whole != c.whole || !slices.Equal(got, c.read) {
			t.Errorf("%s: read %v whole %v, want %v %v", c.value, got, whole, c.read, c.whole)
		}
	}
}

func TestNullLiteral(t *testing.T) {
	for text, want := range map[string]bool{"null": true, "(null)": true, `"null"`: false, "var.x": false, "x ? null : 1": false, "{": false} {
		if nullLiteral(text) != want {
			t.Errorf("%s: want %v", text, want)
		}
	}
}

// An output the change adds or edits that reads a block with count or
// for_each is a candidate with that line in its evidence; an untouched one,
// or one reading a block with no mode switch, is not. A long output cuts the
// evidence at its cap.
func TestCoverageReachModeOutputs(t *testing.T) {
	main := "resource \"aws_demo_widget\" \"this\" {\n  count = 1\n}\n\nresource \"aws_demo_widget\" \"one\" {\n}\n"
	same := "output \"same\" {\n  value = aws_demo_widget.this[0].arn\n}\n"
	head := map[string]string{"main.tf": main, "outputs.tf": same +
		"output \"new\" {\n  value = aws_demo_widget.this[0].id\n}\n" +
		"output \"one\" {\n  value = aws_demo_widget.one.id\n}\n" +
		"output \"long\" {\n  value = [\n" + strings.Repeat("    aws_demo_widget.this[0].id,\n", 10) + "  ]\n}\n"}
	r := runCoverageReach(t, head, map[string]string{"main.tf": main, "outputs.tf": same})
	var got []string
	for _, c := range r.Candidates {
		if !strings.HasPrefix(c.Subject, ".:output.") {
			continue
		}
		got = append(got, c.Subject)
		last := c.Evidence.SourceLines[len(c.Evidence.SourceLines)-1]
		switch c.Subject {
		case ".:output.new":
			if !c.EvidenceComplete || last.File != "main.tf" || last.Line != 2 {
				t.Errorf("new: evidence %+v", c.Evidence.SourceLines)
			}
		case ".:output.long":
			if c.EvidenceComplete || len(c.Evidence.SourceLines) != maxEvidenceLines {
				t.Errorf("long: cap did not bite")
			}
		}
	}
	if !slices.Equal(got, []string{".:output.new", ".:output.long"}) {
		t.Errorf("output candidates %v", got)
	}
}
