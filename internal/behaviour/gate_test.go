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
