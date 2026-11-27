package behaviour

import (
	"encoding/json"
	"testing"
)

// TestResourceGate: a managed resource the change adds carries its count or
// for_each, with the inputs and references it reads; a removed resource, a
// data source, a module call and a resource with neither carry no gate.
func TestResourceGate(t *testing.T) {
	base := map[string]string{"main.tf": `resource "aws_thing" "gone" {
  count = var.old ? 1 : 0
}
variable "old" {
  type    = bool
  default = false
}
`}
	head := map[string]string{"main.tf": `variable "old" {
  type    = bool
  default = false
}
variable "create" {
  type    = bool
  default = false
}
variable "items" {
  type    = map(string)
  default = {}
}
resource "aws_thing" "c" {
  count = var.create ? 1 : 0
}
resource "aws_thing" "f" {
  for_each = var.items
  name     = each.value
}
resource "aws_thing" "mixed" {
  count = var.create && length(aws_thing.f) > 0 ? 1 : 0
}
resource "aws_thing" "plain" {}
data "aws_look" "l" {
  count = var.create ? 1 : 0
}
module "m" {
  source = "./m"
  count  = var.create ? 1 : 0
}
`}
	r := runPair(t, base, head, "")
	type want struct {
		attr, expr string
		inputs     []string
		ctx        []string
	}
	cases := map[string]*want{
		"aws_thing.c":     {"count", "var.create ? 1 : 0", []string{"create"}, []string{}},
		"aws_thing.f":     {"for_each", "var.items", []string{"items"}, []string{}},
		"aws_thing.mixed": {"count", "var.create && length(aws_thing.f) > 0 ? 1 : 0", []string{"create"}, []string{"aws_thing.f"}},
		"aws_thing.plain": nil,
		"aws_thing.gone":  nil,
		"data.aws_look.l": nil,
		"module.m":        nil,
	}
	for addr, w := range cases {
		var tr *Trigger
		for i := range r.Triggers {
			if r.Triggers[i].Category == CatResourceAddRemove && deref(r.Triggers[i].Address) == addr {
				tr = &r.Triggers[i]
			}
		}
		if tr == nil {
			t.Errorf("%s: no resource-added-or-removed trigger", addr)
			continue
		}
		if w == nil {
			if tr.Gate != nil {
				t.Errorf("%s: want no gate, got %+v", addr, *tr.Gate)
			}
			continue
		}
		g := tr.Gate
		if g == nil {
			t.Errorf("%s: no gate", addr)
			continue
		}
		var names []string
		for _, in := range g.ReferencedInputs {
			names = append(names, in.Name)
			if in.Declaration == nil || !in.Declaration.Added {
				t.Errorf("%s: input %s lacks an added declaration", addr, in.Name)
			}
		}
		got, _ := json.Marshal([]any{g.Attribute, deref(g.Expr), names, g.ContextualRefs})
		exp, _ := json.Marshal([]any{w.attr, w.expr, w.inputs, w.ctx})
		if string(got) != string(exp) {
			t.Errorf("%s: gate %s, want %s", addr, got, exp)
		}
		if !g.Pure {
			t.Errorf("%s: gate not pure", addr)
		}
	}
}

// TestResourceGateImpure: a gate that calls a function off the allowlist,
// a provider or core function, reads self, or reads var with no static
// variable name is not pure, and a resource with both count and for_each
// has no gate.
func TestResourceGateImpure(t *testing.T) {
	base := map[string]string{"main.tf": "variable \"create\" {\n  default = false\n}\n"}
	head := map[string]string{"main.tf": `variable "create" {
  default = false
}
resource "aws_thing" "file" {
  count = file("x") == "" ? 0 : 1
}
resource "aws_thing" "prov" {
  count = provider::aws::arn_parse("arn:aws:s3:::b").partition == "aws" ? 0 : 1
}
resource "aws_thing" "core" {
  count = core::fileexists("x") ? 1 : 0
}
resource "aws_thing" "self" {
  count = self.id == "" ? 0 : 1
}
resource "aws_thing" "bare" {
  count = var
}
resource "aws_thing" "index" {
  count = var["create"] ? 1 : 0
}
resource "aws_thing" "both" {
  count    = var.create ? 1 : 0
  for_each = {}
}
`}
	r := runPair(t, base, head, "")
	for _, name := range []string{"file", "prov", "core", "self", "bare", "index", "both"} {
		addr := "aws_thing." + name
		var tr *Trigger
		for i := range r.Triggers {
			if r.Triggers[i].Category == CatResourceAddRemove && deref(r.Triggers[i].Address) == addr {
				tr = &r.Triggers[i]
			}
		}
		switch {
		case tr == nil:
			t.Errorf("%s: no resource-added-or-removed trigger", addr)
		case name == "both":
			if tr.Gate != nil {
				t.Errorf("%s: want no gate, got %+v", addr, *tr.Gate)
			}
		case tr.Gate == nil:
			t.Errorf("%s: no gate", addr)
		case tr.Gate.Pure:
			t.Errorf("%s: gate %q reads as pure", addr, deref(tr.Gate.Expr))
		}
	}
}

// TestMergeDropsGate: a merged trigger stands for more than one resource,
// so it carries no gate.
func TestMergeDropsGate(t *testing.T) {
	addr, expr := "aws_thing.t", "var.x ? 1 : 0"
	g := &Gate{Attribute: "count", Expr: &expr, ReferencedInputs: []Input{}, ContextualRefs: []string{}, Pure: true}
	a := Trigger{Category: CatResourceAddRemove, Address: &addr, Levels: []string{LevelL4}, Gate: g}
	b := Trigger{Category: CatUnclassified, Address: &addr, Levels: []string{LevelL4}}
	m, ok := mergeTwo(a, b)
	if !ok {
		t.Fatal("mergeTwo refused")
	}
	if m.Gate != nil {
		t.Errorf("merged trigger keeps gate %+v", *m.Gate)
	}
}
