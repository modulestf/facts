package behaviour

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// absentSchema has aws_thing at two provider sources: hashicorp/aws with a
// plain attribute "oc" (Optional and Computed), "opt" (Optional), a
// write-only "wo", an attribute "ing" that may be written as blocks, a
// nested attribute type "obj" and a legacy nested block
// "rule"; acme/aws lacks "oc", so a revision that resolves aws to acme/aws
// disagrees about it. aws_look is a data source.
const absentSchema = `{"format_version":"1.0","provider_schemas":{
"registry.terraform.io/hashicorp/aws":{
  "resource_schemas":{"aws_thing":{"version":0,"block":{
    "attributes":{
      "oc":{"type":"string","optional":true,"computed":true},
      "opt":{"type":"string","optional":true},
      "wo":{"type":"string","optional":true,"write_only":true},
      "ing":{"type":["list",["object",{"x":"number"}]],"optional":true,"computed":true},
      "obj":{"nested_type":{"nesting_mode":"single","attributes":{"inner":{"type":"string","optional":true}}},"optional":true}},
    "block_types":{"rule":{"nesting_mode":"list","block":{"attributes":{"x":{"type":"string","optional":true}}}}}}}},
  "data_source_schemas":{"aws_look":{"version":0,"block":{"attributes":{
    "name":{"type":"string","optional":true},
    "id":{"type":"string","computed":true}}}}}},
"registry.terraform.io/acme/aws":{
  "resource_schemas":{"aws_thing":{"version":0,"block":{"attributes":{
    "opt":{"type":"string","optional":true}}}}}}}}`

const varX = `variable "x" {
  type    = string
  default = null
}
`

// atAttr lists the triggers of r at address addr and attribute attr.
func atAttr(r *Report, addr, attr string) []Trigger {
	var out []Trigger
	for _, tr := range r.Triggers {
		if str(tr.Address) == addr && str(tr.Attribute) == attr {
			out = append(out, tr)
		}
	}
	return out
}

func noAbsent(t *testing.T, r *Report) {
	t.Helper()
	for _, tr := range r.Triggers {
		if tr.BaseAbsent || tr.HeadAbsent || str(tr.BaseExpr) == "null" || str(tr.HeadExpr) == "null" {
			t.Errorf("%s %s %s: want no null side, got base %q head %q flags %v %v",
				tr.ID, str(tr.Address), str(tr.Attribute), str(tr.BaseExpr), str(tr.HeadExpr), tr.BaseAbsent, tr.HeadAbsent)
		}
	}
}

// TestAbsentIsNull: an attribute added to, or removed from, a resource the
// schema of both revisions marks plain reads as null at the other side,
// with the flag set after classification: the categories and levels are
// those of the same change with no null side.
func TestAbsentIsNull(t *testing.T) {
	sch := writeJSON(t, "schema.json", absentSchema)
	base := map[string]string{"main.tf": varX + `resource "aws_thing" "t" {
  opt = var.x
}
`}
	head := map[string]string{"main.tf": varX + `resource "aws_thing" "t" {
  oc = var.x
}
`}
	r := runPair(t, base, head, sch)
	added := atAttr(r, "aws_thing.t", "oc")
	if len(added) == 0 {
		t.Fatalf("no trigger for the added attribute: %v", categories(r))
	}
	for _, tr := range added {
		if !tr.BaseAbsent || tr.HeadAbsent || str(tr.BaseExpr) != "null" || str(tr.HeadExpr) != "var.x" || tr.ChangeKind != KindAdded {
			t.Errorf("added %s: base %q head %q flags %v %v kind %s", tr.Category, str(tr.BaseExpr), str(tr.HeadExpr), tr.BaseAbsent, tr.HeadAbsent, tr.ChangeKind)
		}
	}
	removed := atAttr(r, "aws_thing.t", "opt")
	if len(removed) == 0 {
		t.Fatalf("no trigger for the removed attribute: %v", categories(r))
	}
	for _, tr := range removed {
		if tr.BaseAbsent || !tr.HeadAbsent || str(tr.HeadExpr) != "null" || str(tr.BaseExpr) != "var.x" || tr.ChangeKind != KindRemoved {
			t.Errorf("removed %s: base %q head %q flags %v %v", tr.Category, str(tr.BaseExpr), str(tr.HeadExpr), tr.BaseAbsent, tr.HeadAbsent)
		}
	}
	// Same categories and levels as the classification of the bare change.
	got := func(ts []Trigger) []string {
		var out []string
		for _, tr := range ts {
			out = append(out, tr.Category+":"+strings.Join(tr.Levels, ","))
		}
		return out
	}
	if want := []string{CatUnclassified + ":L4", CatUnknownPropagation + ":L1,L3"}; !slices.Equal(got(added), want) {
		t.Errorf("added: want %v, got %v", want, got(added))
	}
	if want := []string{CatUnclassified + ":L4"}; !slices.Equal(got(removed), want) {
		t.Errorf("removed (Optional only, no candidate): want %v, got %v", want, got(removed))
	}
	// The L1 trigger has its cases at both sides.
	for _, tr := range added {
		if tr.Category == CatUnknownPropagation && len(tr.Cases) == 0 {
			t.Errorf("no cases: %s", str(tr.Reason))
		}
	}
	// The controls of the refusals in TestAbsentNotNull: the same provider
	// requirement at both revisions, and "ing" absent in any syntax.
	req := "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \">= 5.0\"\n    }\n  }\n}\n"
	for name, c := range map[string][2]string{
		"same requirement": {req + varX + "resource \"aws_thing\" \"t\" {}\n", req + varX + "resource \"aws_thing\" \"t\" {\n  oc = var.x\n}\n"},
		"attribute ing":    {"resource \"aws_thing\" \"t\" {}\n", "resource \"aws_thing\" \"t\" {\n  ing = [{ x = 1 }]\n}\n"},
	} {
		r := runPair(t, map[string]string{"main.tf": c[0]}, map[string]string{"main.tf": c[1]}, sch)
		if !slices.ContainsFunc(r.Triggers, func(tr Trigger) bool { return tr.BaseAbsent && str(tr.BaseExpr) == "null" }) {
			t.Errorf("%s: want a null base: %v", name, categories(r))
		}
	}
	// A literal added: still literal-argument-change, with the null side.
	r = runPair(t, map[string]string{"main.tf": `resource "aws_thing" "t" {}` + "\n"},
		map[string]string{"main.tf": `resource "aws_thing" "t" {
  oc = "a"
}
`}, sch)
	lit := atAttr(r, "aws_thing.t", "oc")
	if len(lit) != 1 || lit[0].Category != CatLiteral || !lit[0].BaseAbsent || str(lit[0].BaseExpr) != "null" {
		t.Errorf("literal: %+v", lit)
	}
}

// TestAbsentNotNull: each condition of the null side, broken one at a
// time, leaves the side absent.
func TestAbsentNotNull(t *testing.T) {
	sch := writeJSON(t, "schema.json", absentSchema)
	res := func(body string) string { return varX + "resource \"aws_thing\" \"t\" {\n" + body + "}\n" }
	acme := `terraform {
  required_providers {
    aws = {
      source = "acme/aws"
    }
  }
}
`
	req := func(version string) string {
		return "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"" + version + "\"\n    }\n  }\n}\n"
	}
	blocks := "  ing {\n    x = 1\n  }\n"
	dynamic := "  dynamic \"ing\" {\n    for_each = [1]\n    content {\n      x = ing.value\n    }\n  }\n"
	attr := "  ing = [{ x = 1 }]\n"
	cases := []struct {
		name       string
		base, head string
		schema     string
	}{
		{"no schema", res(""), res("  oc = var.x\n"), ""},
		{"provider version constraint changed", req(">= 5.0") + res(""), req(">= 6.0") + res("  oc = var.x\n"), sch},
		{"attribute as blocks, added", res(blocks), res(attr), sch},
		{"attribute as blocks, removed", res(attr), res(blocks), sch},
		{"attribute as a dynamic block, added", res(dynamic), res(attr), sch},
		{"attribute as a dynamic block, removed", res(attr), res(dynamic), sch},
		{"schema disagreement", acme + res(""), res("  oc = var.x\n"), sch},
		{"write-only", res(""), res("  wo = var.x\n"), sch},
		{"not in the schema", res(""), res("  nope = var.x\n"), sch},
		{"legacy nested block", res("  rule {\n  }\n"), res("  rule {\n    x = var.x\n  }\n"), sch},
		{"missing parent", res(""), res("  rule {\n    x = var.x\n  }\n"), sch},
		{"resource added", varX, res("  oc = var.x\n"), sch},
		{"count", res(""), res("  count = 1\n"), sch},
		{"for_each", res(""), res("  for_each = toset([var.x])\n"), sch},
		{"depends_on", res(""), res("  depends_on = [terraform_data.d]\n"), sch},
		{"provider", res(""), res("  provider = aws.alt\n"), sch},
		{"lifecycle", res(""), res("  lifecycle {\n    create_before_destroy = true\n  }\n"), sch},
		{"lifecycle argument", res("  lifecycle {\n  }\n"), res("  lifecycle {\n    ignore_changes = [oc]\n  }\n"), sch},
		{"provisioner", res(""), res("  provisioner \"local-exec\" {\n    command = var.x\n  }\n"), sch},
		{"connection", res(""), res("  connection {\n    host = var.x\n  }\n"), sch},
		{"dynamic", res(""), res("  dynamic \"rule\" {\n    for_each = [var.x]\n    content {\n      x = rule.value\n    }\n  }\n"), sch},
		{"moved", res(""), res("") + "moved {\n  from = aws_thing.u\n  to   = aws_thing.t\n}\n", sch},
		{"import", res(""), res("") + "import {\n  to = aws_thing.t\n  id = var.x\n}\n", sch},
		{"removed", res(""), res("") + "removed {\n  from = aws_thing.old\n}\n", sch},
		{"check", res(""), res("") + "check \"c\" {\n  assert {\n    condition     = var.x != \"\"\n    error_message = \"x\"\n  }\n}\n", sch},
		{"provider block", varX + "provider \"aws\" {}\n", varX + "provider \"aws\" {\n  region = var.x\n}\n", sch},
		{"module block", varX + "module \"m\" {\n  source = \"./m\"\n}\n", varX + "module \"m\" {\n  source = \"./m\"\n  oc     = var.x\n}\n", sch},
		{"output block", "output \"o\" {\n  value = 1\n}\n", "output \"o\" {\n  value     = 1\n  sensitive = true\n}\n", sch},
		{"locals block", "locals {\n  a = 1\n}\n", "locals {\n  a = 1\n  b = 2\n}\n", sch},
		{"variable block", varX, strings.Replace(varX, "default", "nullable = false\n  default", 1), sch},
		{"terraform block", "terraform {\n}\n", "terraform {\n  required_version = \">= 1.5\"\n}\n", sch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, map[string]string{"main.tf": c.base}, map[string]string{"main.tf": c.head}, c.schema)
			if len(r.Triggers) == 0 {
				t.Fatal("the change has no trigger")
			}
			noAbsent(t, r)
		})
	}
}

// caseLabels lists every state label of the case ids of tr.
func caseLabels(tr Trigger) []string {
	var out []string
	for _, c := range tr.Cases {
		out = append(out, strings.FieldsFunc(c.ID, func(r rune) bool { return r == '.' || r == ':' })...)
	}
	return out
}

// TestDefaultCase: a variable the change adds gives the triggers that read
// it a default case only when its default is a bare literal.
func TestDefaultCase(t *testing.T) {
	sch := writeJSON(t, "schema.json", absentSchema)
	run := func(base, def string) Trigger {
		t.Helper()
		head := "variable \"x\" {\n  type    = string\n  default = " + def + "\n}\nresource \"aws_thing\" \"t\" {\n  oc = var.x\n}\n"
		r := runPair(t, map[string]string{"main.tf": base + "resource \"aws_thing\" \"t\" {}\n"}, map[string]string{"main.tf": head}, sch)
		for _, tr := range atAttr(r, "aws_thing.t", "oc") {
			if tr.Category == CatUnknownPropagation {
				return tr
			}
		}
		t.Fatalf("no candidate: %v", categories(r))
		return Trigger{}
	}
	defaultValue := func(tr Trigger) (json.RawMessage, bool) {
		for _, c := range tr.Cases {
			if slices.Contains(strings.FieldsFunc(c.ID, func(r rune) bool { return r == '.' || r == ':' }), "default") {
				return c.Inputs["x"].Value, true
			}
		}
		return nil, false
	}
	if v, ok := defaultValue(run("", `"gp3"`)); !ok || string(v) != `"gp3"` {
		t.Errorf("bare string default: %s %v", v, ok)
	}
	for _, def := range []string{`"${var.y}-a"`, `"%{if true}a%{endif}"`, `"$${x}"`, `"%%{x}"`, `upper("a")`, `3`, "<<EOT\ngp3\nEOT"} {
		if tr := run("variable \"y\" {}\n", def); slices.Contains(caseLabels(tr), "default") {
			t.Errorf("%s: a non-literal default gives no default case: %v", def, caseLabels(tr))
		}
	}
	// A null default is the null case already: no second case.
	if tr := run("", "null"); slices.Contains(caseLabels(tr), "default") {
		t.Errorf("null default: %v", caseLabels(tr))
	}
	// An existing variable: no default case, even with a bare literal.
	existing := "variable \"x\" {\n  type    = string\n  default = \"gp2\"\n}\n"
	if tr := run(existing, `"gp3"`); slices.Contains(caseLabels(tr), "default") {
		t.Errorf("existing variable: %v", caseLabels(tr))
	}
}

// TestDataSourceL1Only: a data source argument is a candidate at L1 only,
// whatever the schema says, with the reason that names what is not
// modelled; a literal on both sides is none.
func TestDataSourceL1Only(t *testing.T) {
	sch := writeJSON(t, "schema.json", absentSchema)
	base := map[string]string{"main.tf": varX + "data \"aws_look\" \"l\" {\n  name = \"a\"\n}\n"}
	head := map[string]string{"main.tf": varX + "data \"aws_look\" \"l\" {\n  name = var.x\n}\n"}
	for _, s := range []string{"", sch} {
		r := runPair(t, base, head, s)
		var cand []Trigger
		for _, tr := range r.Triggers {
			if slices.Contains(tr.Levels, LevelL3) {
				t.Errorf("schema %q: %s selects L3", s, tr.ID)
			}
			if tr.Category == CatUnknownPropagation || slices.Contains(tr.AlsoCategories, CatUnknownPropagation) {
				cand = append(cand, tr)
			}
		}
		// The candidate may merge into the trigger of the same expression,
		// whose levels then join: L1 is there, L3 never.
		if len(cand) != 1 || !slices.Contains(cand[0].Levels, LevelL1) {
			t.Fatalf("schema %q: want one candidate at L1, got %+v", s, cand)
		}
		if !strings.Contains(str(cand[0].Reason), "depends_on") || !strings.Contains(str(cand[0].Reason), "not planned at L3") {
			t.Errorf("reason: %s", str(cand[0].Reason))
		}
	}
	lit := map[string]string{"main.tf": "data \"aws_look\" \"l\" {\n  name = \"b\"\n}\n"}
	if r := runPair(t, map[string]string{"main.tf": "data \"aws_look\" \"l\" {\n  name = \"a\"\n}\n"}, lit, sch); slices.Contains(categories(r), CatUnknownPropagation) {
		t.Errorf("a literal change: %v", categories(r))
	}
	// An added data source attribute the schema marks plain gets its null side.
	r := runPair(t, map[string]string{"main.tf": varX + "data \"aws_look\" \"l\" {}\n"}, head, sch)
	for _, tr := range atAttr(r, "data.aws_look.l", "name") {
		if !tr.BaseAbsent || str(tr.BaseExpr) != "null" {
			t.Errorf("added data attribute %s: %q %v", tr.Category, str(tr.BaseExpr), tr.BaseAbsent)
		}
	}
}

// TestAbsentClaimsAndSweep: with the null side a base claim is valid, and
// the sweep pairs the base null with the head, verified where the default
// equals it and outstanding elsewhere.
func TestAbsentClaimsAndSweep(t *testing.T) {
	sch := writeJSON(t, "schema.json", absentSchema)
	b := runPair(t, map[string]string{"main.tf": varX + "resource \"aws_thing\" \"t\" {}\n"},
		map[string]string{"main.tf": varX + "resource \"aws_thing\" \"t\" {\n  oc = var.x\n}\n"}, sch)
	var id string
	for _, tr := range b.Triggers {
		if tr.Category == CatUnknownPropagation {
			id = tr.ID
		}
	}
	if id == "" {
		t.Fatal("no candidate")
	}
	base := claim("C-1", PredExprEquals, `{"node":"`+id+`","revision":"base","inputs":{"x":"a"},"expected":null}`)
	valid, discarded, err := ReadClaims(writeJSON(t, "claims.json", claimsDoc(base)), b)
	if err != nil || len(valid) != 1 || len(discarded) != 0 {
		t.Fatalf("a base claim on a null side: %v %v %v", err, valid, discarded)
	}
	nullCase := `{"case":"null","known":true,"unknown_mask":false,"value":null,"inputs":{"x":{"value":null,"unknown":false,"null":true,"omitted":false}}}`
	typBase := `{"case":"typical","known":true,"unknown_mask":false,"value":null,"inputs":{"x":{"value":"a","unknown":false,"null":false,"omitted":false}}}`
	typHead := `{"case":"typical","known":true,"unknown_mask":false,"value":"a","inputs":{"x":{"value":"a","unknown":false,"null":false,"omitted":false}}}`
	withCases(t, b, id, nullCase, typBase)
	for i := range b.Triggers {
		if b.Triggers[i].ID == id {
			b.Triggers[i].Levels = []string{LevelL1}
		}
	}
	stateOf := func(r *EvalReport) TriggerState {
		for _, s := range r.Triggers {
			if s.ID == id {
				return s
			}
		}
		return TriggerState{}
	}
	// The null case equal at both sides, the typical case differs.
	l := ladder(t, sweepRun(id, "L1", nullCase+","+typBase, nullCase+","+typHead), noCoverage)
	s := stateOf(evalWith(t, b, l))
	if s.State != StateUnverifiable || s.Complete == nil || !*s.Complete || !slices.Equal(s.Outstanding, []string{id + "|L1|typical|value"}) {
		t.Errorf("unexplained: %+v", s)
	}
	// Claims at both sides explain it.
	head := claim("C-2", PredExprEquals, `{"node":"`+id+`","revision":"head","inputs":{"x":"a"},"expected":"a"}`)
	s = stateOf(evalWith(t, b, l, base, head))
	if s.State != StateVerified || len(s.Outstanding) != 0 {
		t.Errorf("explained: %+v", s)
	}
}
