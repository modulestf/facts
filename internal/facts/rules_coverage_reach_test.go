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
// binds, after each edit of the decoded handoff.
func runCoverageReach(t *testing.T, head, base map[string]string, edits ...func(h map[string]any)) *Report {
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
	for _, edit := range edits {
		edit(h)
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
		{"aws_demo_widget.this.listener[0].arn", []string{"listener.arn"}, false},
		{"aws_demo_widget.this[0].listener[*].arn", []string{"listener.arn"}, false},
		{"aws_demo_widget.this.listener[*].port", []string{"listener.port"}, false},
		{"aws_demo_widget.this.listener.*.port", []string{"listener.port"}, false},
		{"aws_demo_widget.this.listener[var.k].port", []string{"listener.port"}, false},
		{"[for l in aws_demo_widget.this.listener : l.port]", nil, false},
		{"aws_demo_widget.this[var.k]", nil, true},
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
	// Whether each path's value reaches the output unchanged: only then does
	// it read the levels above and below it.
	for value, want := range map[string]map[string]bool{
		"aws_demo_widget.this.listener":                                              {"listener": true},
		"var.c ? aws_demo_widget.this.listener : null":                               {"listener": true},
		"{ l = [aws_demo_widget.this.listener] }":                                    {"listener": true},
		"coalesce(aws_demo_widget.this.config, {})":                                  {"config": true},
		"one(aws_demo_widget.this.listener).port":                                    {"listener": false},
		"tolist(aws_demo_widget.this.listener)[0].port":                              {"listener": false},
		"element(aws_demo_widget.this.listener, 0).port":                             {"listener": false},
		"(var.c ? aws_demo_widget.this.listener : [])[0].port":                       {"listener": false},
		"try(aws_demo_widget.this.listener, null)[0].port":                           {"listener": false},
		"length(aws_demo_widget.this.listener)":                                      {"listener": false},
		`lookup(aws_demo_widget.this.config, "status", null)`:                        {"config": false},
		"element(aws_demo_widget.this[*].arn, 0)":                                    {"arn": false},
		"var.c ? one(aws_demo_widget.this.listener) : aws_demo_widget.this.listener": {"listener": true},
	} {
		read := map[string]bool{}
		outputAttrs(Expr{Text: value, Range: hcl.Range{Start: hcl.InitialPos}}, "outputs.tf", r, read)
		if !maps.Equal(read, want) {
			t.Errorf("%s: read %v, want %v", value, read, want)
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

// A Required argument set from a local is reachable only when the local can
// be followed to a value that is not the literal null.
func TestCoverageReachRequiredLocal(t *testing.T) {
	for _, c := range []struct {
		locals, name string
		want         bool
	}{
		{"n = null", "local.n", true},
		{"m = null\n  n = local.m", "local.n", true},
		{"n = var.name", "local.n", false},
		{"n = \"a\"", "(local.n)", false},
		{"n = { a = 1 }", "local.n.a", true},
		{"", "local.missing", true},
		{"n = local.m\n  m = local.n", "local.n", true},
		{"", "var.name", false},
	} {
		head := map[string]string{"main.tf": "locals {\n  " + c.locals + "\n}\n\nresource \"aws_demo_widget\" \"this\" {\n  name = " + c.name +
			"\n  listener {\n    port = 1\n  }\n}\n"}
		r := runCoverageReach(t, head, map[string]string{})
		got := slices.ContainsFunc(r.Candidates, func(c Candidate) bool {
			return c.RuleID == "coverage.required-argument-unreachable" && c.Subject == ".:aws_demo_widget.this.name"
		})
		if got != c.want {
			t.Errorf("locals %q, name = %s: candidate %v, want %v", c.locals, c.name, got, c.want)
		}
	}
}

// An output reading a counted resource through locals, or a module call with
// for_each, is in a module with a mode switch.
func TestCoverageReachModeThroughLocalsAndModules(t *testing.T) {
	main := "locals {\n  a = local.b\n  b = aws_demo_widget.this[0].arn\n}\n\n" +
		"resource \"aws_demo_widget\" \"this\" {\n  count = 1\n}\n\n" +
		"module \"child\" {\n  source   = \"./modules/child\"\n  for_each = {}\n}\n\n" +
		"module \"plain\" {\n  source = \"./modules/child\"\n}\n"
	out := "output \"via_local\" {\n  value = local.a\n}\n" +
		"output \"via_module\" {\n  value = module.child\n}\n" +
		"output \"plain\" {\n  value = module.plain.id\n}\n"
	r := runCoverageReach(t, map[string]string{"main.tf": main, "outputs.tf": out}, map[string]string{})
	var got []string
	for _, c := range r.Candidates {
		if strings.HasPrefix(c.Subject, ".:output.") {
			got = append(got, c.Subject)
		}
	}
	if !slices.Equal(got, []string{".:output.via_local", ".:output.via_module"}) {
		t.Errorf("output candidates %v", got)
	}
}

// A computed attribute inside a nested block or a nested attribute of an
// added resource is a candidate at its nested address unless an output reads
// it, a level above it or a level below it; a deprecated one is not.
func TestCoverageReachNestedComputed(t *testing.T) {
	nested := func(h map[string]any) {
		for _, p := range h["provider_schemas"].([]any) {
			b := p.(map[string]any)["resource_schemas"].(map[string]any)["aws_demo_widget"].(map[string]any)["block"].(map[string]any)
			listener := b["block_types"].(map[string]any)["listener"].(map[string]any)["block"].(map[string]any)["attributes"].(map[string]any)
			listener["listener_arn"] = map[string]any{"computed": true}
			listener["old_listener_arn"] = map[string]any{"computed": true, "deprecated": true}
			b["block_types"].(map[string]any)["legacy_block"] = map[string]any{"nesting_mode": "list", "block": map[string]any{
				"deprecated": true, "attributes": map[string]any{"legacy_arn": map[string]any{"computed": true}}}}
			config := b["attributes"].(map[string]any)["config"].(map[string]any)["nested_type"].(map[string]any)["attributes"].(map[string]any)
			config["status"] = map[string]any{"computed": true}
		}
	}
	main := "resource \"aws_demo_widget\" \"this\" {\n  name = var.name\n  listener {\n    port = 1\n  }\n}\n"
	top := "output \"arn\" {\n  value = aws_demo_widget.this.arn\n}\noutput \"id\" {\n  value = aws_demo_widget.this.id\n}\n"
	for _, c := range []struct {
		outputs string
		want    []string
	}{
		{top, []string{".:aws_demo_widget.this.config.status", ".:aws_demo_widget.this.listener.listener_arn"}},
		{top + "output \"l\" {\n  value = aws_demo_widget.this.listener[0].listener_arn\n}\n" +
			"output \"c\" {\n  value = aws_demo_widget.this.config\n}\n", nil},
		{top + "output \"l\" {\n  value = aws_demo_widget.this.listener\n}\n" +
			"output \"c\" {\n  value = aws_demo_widget.this.config.status.x\n}\n", nil},
		{top + "output \"a\" {\n  value = aws_demo_widget.this.listener[*].port\n}\n" +
			"output \"b\" {\n  value = aws_demo_widget.this.listener.*.port\n}\n" +
			"output \"c\" {\n  value = aws_demo_widget.this.listener[var.k].port\n}\n" +
			"output \"d\" {\n  value = [for l in aws_demo_widget.this.listener : l.port]\n}\n",
			[]string{".:aws_demo_widget.this.config.status", ".:aws_demo_widget.this.listener.listener_arn"}},
		{top + "output \"a\" {\n  value = one(aws_demo_widget.this.listener).port\n}\n" +
			"output \"b\" {\n  value = tolist(aws_demo_widget.this.listener)[0].port\n}\n" +
			"output \"c\" {\n  value = element(aws_demo_widget.this.listener, 0).port\n}\n" +
			"output \"d\" {\n  value = (var.c ? aws_demo_widget.this.listener : [])[0].port\n}\n" +
			"output \"e\" {\n  value = try(aws_demo_widget.this.listener, null)[0].port\n}\n" +
			"output \"f\" {\n  value = length(aws_demo_widget.this.listener)\n}\n" +
			"output \"g\" {\n  value = lookup(aws_demo_widget.this.config, \"status\", null)\n}\n",
			[]string{".:aws_demo_widget.this.config.status", ".:aws_demo_widget.this.listener.listener_arn"}},
		{top + "output \"l\" {\n  value = var.c ? try(aws_demo_widget.this.listener, null) : null\n}\n" +
			"output \"c\" {\n  value = { c = aws_demo_widget.this.config }\n}\n", nil},
	} {
		r := runCoverageReach(t, map[string]string{"main.tf": main, "outputs.tf": c.outputs}, map[string]string{}, nested)
		var got []string
		for _, cand := range r.Candidates {
			if cand.RuleID == "coverage.attribute-not-output" {
				got = append(got, cand.Subject)
			}
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("outputs %q: candidates %v, want %v", c.outputs, got, c.want)
		}
	}
}
