package facts

import (
	"reflect"
	"strings"
	"testing"
)

const parseSrc = `terraform {
  required_providers {
    aws = { source = "hashicorp/aws" }
  }
}

provider "aws" {
  alias = "other"
}

variable "typed" {
  type        = string
  description = "Name of the thing."
}

# Accepts a list or a map.
variable "loose" {
  type = any
}

variable "nested" {
  type        = map(object({ x = optional(any) }))
  description = ""
}

resource "aws_thing" "this" {
  count    = var.create ? 1 : 0
  provider = aws.other
  name     = var.typed

  dynamic "rule" {
    for_each = var.rules
    content {}
  }
  setting {}
  lifecycle {
    ignore_changes = [name]
  }
  depends_on = [aws_thing.other]
}

data "aws_partition" "current" {}

moved {
  from = aws_thing.old
  to   = aws_thing.this[0]
}

removed {
  from = aws_thing.gone
}

module "child" {
  source   = "./modules/child"
  for_each = var.items
  value    = each.value
}

output "id" {
  value = try(aws_thing.this[0].id, null)
}
`

// parseOne parses src as main.tf of a one-directory tree.
func parseOne(t *testing.T, src string) *Module {
	t.Helper()
	m, err := ParseDir(mustTree(t, writeTree(t, map[string]string{"main.tf": src})), ".", DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Unsupported) > 0 {
		t.Fatal(m.Unsupported)
	}
	return m
}

// text returns the source text of e, or "<nil>".
func text(e *Expr) string {
	if e == nil {
		return "<nil>"
	}
	return e.Text
}

func TestParseDir(t *testing.T) {
	m := parseOne(t, parseSrc)
	type varView struct {
		Name, File, TypeText          string
		Line                          int
		HasType, UsesAny, Description bool
		Comments                      []Comment
	}
	var gotVars []varView
	for _, v := range m.Variables {
		gotVars = append(gotVars, varView{v.Name, v.File, v.TypeText, v.Line, v.HasType, v.TypeUsesAny, v.HasDescription, v.Comments})
	}
	wantVars := []varView{
		{Name: "typed", File: "main.tf", Line: 11, HasType: true, TypeText: "string", Description: true},
		{Name: "loose", File: "main.tf", Line: 17, HasType: true, TypeText: "any", UsesAny: true, Comments: []Comment{{Line: 16, Text: "# Accepts a list or a map."}}},
		{Name: "nested", File: "main.tf", Line: 21, HasType: true, TypeText: "map(object({ x = optional(any) }))", UsesAny: true},
	}
	if !reflect.DeepEqual(gotVars, wantVars) {
		t.Errorf("variables\n got %+v\nwant %+v", gotVars, wantVars)
	}
	if !reflect.DeepEqual(m.RequiredProviders, []string{"aws"}) || len(m.Providers) != 1 || m.Providers[0].Alias != `"other"` {
		t.Errorf("providers %v %+v", m.RequiredProviders, m.Providers)
	}
	r := m.Resources[0]
	if !reflect.DeepEqual(r.Meta, []string{"count", "depends_on", "dynamic", "lifecycle", "provider"}) ||
		!reflect.DeepEqual(r.Attributes, []string{"name"}) || !reflect.DeepEqual(r.Blocks, []string{"rule", "setting"}) {
		t.Errorf("resource %+v", r)
	}
	if m.Resources[1].Mode != "data" || m.Resources[1].Type != "aws_partition" {
		t.Errorf("data %+v", m.Resources[1])
	}
	if m.Moved[0].From != "aws_thing.old" || m.Moved[0].To != "aws_thing.this[0]" || m.Removed[0].From != "aws_thing.gone" {
		t.Errorf("moved %+v removed %+v", m.Moved, m.Removed)
	}
	c := m.ModuleCalls[0]
	if c.Source != "./modules/child" || c.SourceRange.Start.Line != 54 || !reflect.DeepEqual(c.Meta, []string{"for_each", "source"}) ||
		!reflect.DeepEqual(c.Attributes, []string{"value"}) {
		t.Errorf("module call %+v", c)
	}
	if o := m.Outputs[0]; o.Name != "id" || o.HasDescription || o.Line != 59 {
		t.Errorf("output %+v", o)
	}
}

// TestParseConstructs checks the source text and ranges the parser keeps,
// one construct per case.
func TestParseConstructs(t *testing.T) {
	for name, tc := range map[string]struct {
		src   string
		check func(t *testing.T, m *Module)
	}{
		"variable arguments": {
			src: `variable "x" {
  type      = number
  default   = 3
  nullable  = false
  sensitive = true
  validation {
    condition     = var.x > 0
    error_message = "Must be positive."
  }
  validation {
    condition     = var.x < 10
    error_message = "Too big."
  }
}
`,
			check: func(t *testing.T, m *Module) {
				v := m.Variables[0]
				if text(v.Default) != "3" || text(v.Nullable) != "false" || text(v.Sensitive) != "true" || v.TypeText != "number" {
					t.Errorf("arguments %s %s %s %s", text(v.Default), text(v.Nullable), text(v.Sensitive), v.TypeText)
				}
				if v.Default.Range.Start.Line != 3 || v.Range.Start.Line != 1 || v.Range.End.Line != 14 {
					t.Errorf("ranges %v %v", v.Default.Range, v.Range)
				}
				if len(v.Validations) != 2 || text(v.Validations[0].Condition) != "var.x > 0" ||
					text(v.Validations[1].ErrorMessage) != `"Too big."` || v.Validations[1].Range.Start.Line != 10 {
					t.Errorf("validations %+v", v.Validations)
				}
			},
		},
		"absent variable arguments": {
			src: `variable "x" {}` + "\n",
			check: func(t *testing.T, m *Module) {
				v := m.Variables[0]
				if v.Default != nil || v.Nullable != nil || v.Sensitive != nil || v.HasType || v.Validations != nil || v.TypeAttrs != nil {
					t.Errorf("%+v", v)
				}
			},
		},
		"optional defaults and any": {
			src: `variable "x" {
  type = object({
    a = optional(string, "fast")
    b = optional(any)
    c = object({
      d = optional(number, 1)
    })
    e = list(object({ f = string }))
    "g" = map(any)
  })
}
`,
			check: func(t *testing.T, m *Module) {
				v := m.Variables[0]
				if !v.TypeUsesAny {
					t.Error("any not seen")
				}
				type view struct {
					Path, Default string
					Optional      bool
					Line          int
				}
				var got []view
				for _, ta := range v.TypeAttrs {
					got = append(got, view{ta.Path, text(ta.Default), ta.Optional, ta.Range.Start.Line})
				}
				want := []view{
					{"a", `"fast"`, true, 3},
					{"b", "<nil>", true, 4},
					{"c", "<nil>", false, 5},
					{"c.d", "1", true, 6},
					{"e", "<nil>", false, 8},
					{"e.f", "<nil>", false, 8},
					{"g", "<nil>", false, 9},
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("type attributes\n got %+v\nwant %+v", got, want)
				}
			},
		},
		"any with no object": {
			src: "variable \"x\" {\n  type = list(any)\n}\n",
			check: func(t *testing.T, m *Module) {
				if v := m.Variables[0]; !v.TypeUsesAny || v.TypeAttrs != nil {
					t.Errorf("%+v", v)
				}
			},
		},
		"output value, references and try": {
			src: `output "x" {
  value     = try(module.a.id, data.aws_x.y.arn, aws_z.this[0].id, var.v, local.l, each.key, path.module)
  sensitive = true
}

output "y" {
  value = var.v
}
`,
			check: func(t *testing.T, m *Module) {
				o := m.Outputs[0]
				want := []string{"aws_z.this", "data.aws_x.y", "local.l", "module.a", "var.v"}
				if !reflect.DeepEqual(o.References, want) || !o.UsesTry || text(o.Sensitive) != "true" ||
					!strings.HasPrefix(o.Value.Text, "try(") || o.Value.Range.Start.Line != 2 {
					t.Errorf("%+v", o)
				}
				if o := m.Outputs[1]; o.UsesTry || !reflect.DeepEqual(o.References, []string{"var.v"}) || o.Sensitive != nil {
					t.Errorf("%+v", o)
				}
			},
		},
		"locals": {
			src: "locals {\n  b = 2\n  a = {\n    k = var.x\n  }\n}\n",
			check: func(t *testing.T, m *Module) {
				if len(m.Locals) != 2 || m.Locals[0].Name != "b" || m.Locals[1].Name != "a" ||
					m.Locals[1].Range.Start.Line != 3 || m.Locals[1].Range.End.Line != 5 || m.Locals[0].Value.Text != "2" {
					t.Errorf("%+v", m.Locals)
				}
			},
		},
		"terraform block": {
			src: `terraform {
  required_version = ">= 1.5.7"
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      version               = ">= 6.0"
      configuration_aliases = [aws.replica, aws.dr]
    }
    legacy = "~> 1.0"
  }
}
`,
			check: func(t *testing.T, m *Module) {
				tf := m.Terraform[0]
				if text(tf.RequiredVersion) != `">= 1.5.7"` || tf.RequiredVersion.Range.Start.Line != 2 {
					t.Errorf("required_version %v", tf.RequiredVersion)
				}
				want := []RequiredProvider{
					{Name: "aws", Source: "hashicorp/aws", Version: ">= 6.0", Aliases: []string{"aws.replica", "aws.dr"}},
					{Name: "legacy", Version: "~> 1.0"},
				}
				for i := range tf.RequiredProviders {
					tf.RequiredProviders[i].Range = want[i].Range
				}
				if !reflect.DeepEqual(tf.RequiredProviders, want) || !reflect.DeepEqual(m.RequiredProviders, []string{"aws", "legacy"}) {
					t.Errorf("required_providers %+v %v", tf.RequiredProviders, m.RequiredProviders)
				}
			},
		},
		"provider block": {
			src: "provider \"aws\" {\n  alias  = \"replica\"\n  region = var.region\n}\n",
			check: func(t *testing.T, m *Module) {
				p := m.Providers[0]
				if p.Alias != `"replica"` || text(p.Body.Arg("region")) != "var.region" || p.Range.End.Line != 4 {
					t.Errorf("%+v", p)
				}
			},
		},
		"nested blocks and dynamic": {
			src: `resource "aws_x" "this" {
  name = "a"
  rule {
    id = 1
    filter {
      prefix = "p"
    }
  }
  dynamic "rule" {
    for_each = var.rules
    iterator = r
    content {
      id = r.value.id
    }
  }
}
`,
			check: func(t *testing.T, m *Module) {
				b := m.Resources[0].Body
				if len(b.Attributes) != 1 || b.Attributes[0].Value.Text != `"a"` || len(b.Blocks) != 2 {
					t.Fatalf("%+v", b)
				}
				static, dyn := b.Blocks[0], b.Blocks[1]
				if static.Dynamic || static.Type != "rule" || text(static.Body.Arg("id")) != "1" ||
					text(static.Body.Blocks[0].Body.Arg("prefix")) != `"p"` || static.Body.Blocks[0].Range.Start.Line != 5 {
					t.Errorf("static %+v", static)
				}
				if !dyn.Dynamic || dyn.Type != "rule" || text(dyn.ForEach) != "var.rules" || dyn.Iterator != "r" ||
					text(dyn.Body.Arg("id")) != "r.value.id" || dyn.Range.Start.Line != 9 || dyn.Range.End.Line != 15 {
					t.Errorf("dynamic %+v", dyn)
				}
			},
		},
		"count to for_each": {
			src: `resource "aws_x" "a" {
  count = var.create ? 1 : 0
}

resource "aws_x" "b" {
  for_each = var.items
}
`,
			check: func(t *testing.T, m *Module) {
				a, b := m.Resources[0], m.Resources[1]
				if text(a.Body.Arg("count")) != "var.create ? 1 : 0" || a.Body.Arg("for_each") != nil ||
					text(b.Body.Arg("for_each")) != "var.items" || b.Body.Arg("count") != nil {
					t.Errorf("%+v %+v", a.Body, b.Body)
				}
				if !reflect.DeepEqual(a.Meta, []string{"count"}) || !reflect.DeepEqual(b.Meta, []string{"for_each"}) {
					t.Errorf("meta %v %v", a.Meta, b.Meta)
				}
			},
		},
		"moved with expression keys": {
			src: `moved {
  from = aws_x.this[0]
  to   = aws_x.this["a"]
}

moved {
  from = module.m["k"].aws_x.y
  to   = aws_x.this[var.k]
}

moved {
  from = aws_x.this["${var.k}"]
  to   = aws_x.this[local.keys[0]]
}

removed {
  from = aws_x.gone
  lifecycle {
    destroy = false
  }
}
`,
			check: func(t *testing.T, m *Module) {
				type view struct{ From, To, FromAddr, ToAddr string }
				var got []view
				for _, mv := range m.Moved {
					got = append(got, view{mv.From, mv.To, mv.FromAddr, mv.ToAddr})
				}
				want := []view{
					{"aws_x.this[0]", `aws_x.this["a"]`, "aws_x.this[0]", `aws_x.this["a"]`},
					{`module.m["k"].aws_x.y`, "aws_x.this[var.k]", `module.m["k"].aws_x.y`, ""},
					{`aws_x.this["${var.k}"]`, "aws_x.this[local.keys[0]]", "", ""},
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("moved\n got %+v\nwant %+v", got, want)
				}
				rm := m.Removed[0]
				if rm.FromAddr != "aws_x.gone" || rm.Body.Blocks[0].Type != "lifecycle" || text(rm.Body.Blocks[0].Body.Arg("destroy")) != "false" {
					t.Errorf("removed %+v", rm)
				}
			},
		},
		"module call arguments": {
			src: "module \"m\" {\n  source  = \"org/name/aws\"\n  version = \"1.0.0\"\n  name    = var.name\n  tags    = merge(var.tags, {})\n}\n",
			check: func(t *testing.T, m *Module) {
				c := m.ModuleCalls[0]
				var names []string
				for _, a := range c.Body.Attributes {
					names = append(names, a.Name)
				}
				if !reflect.DeepEqual(names, []string{"source", "version", "name", "tags"}) ||
					text(c.Body.Arg("tags")) != "merge(var.tags, {})" || c.Body.Attributes[3].Range.Start.Line != 5 {
					t.Errorf("%+v", c.Body)
				}
			},
		},
		"other blocks": {
			src: "locals {}\n\ncheck \"health\" {\n  assert {\n    condition = true\n  }\n}\n\nimport {\n  to = aws_x.a\n  id = \"i-1\"\n}\n",
			check: func(t *testing.T, m *Module) {
				type view struct {
					Type, Labels string
					Line         int
				}
				var got []view
				for _, o := range m.Other {
					got = append(got, view{o.Type, strings.Join(o.Labels, ","), o.Line})
				}
				want := []view{{"locals", "", 1}, {"check", "health", 3}, {"import", "", 9}}
				if !reflect.DeepEqual(got, want) || text(m.Other[2].Body.Arg("id")) != `"i-1"` ||
					text(m.Other[1].Body.Blocks[0].Body.Arg("condition")) != "true" {
					t.Errorf("other\n got %+v\nwant %+v", got, want)
				}
			},
		},
		"block hash": {
			src: "variable \"a\" {\n  type = string\n}\n\nvariable \"b\" {\n  type = string\n}\n\nvariable \"a\" {\n  type  = string\n}\n",
			check: func(t *testing.T, m *Module) {
				h := func(i int) string { return m.Variables[i].Hash }
				if len(h(0)) != 64 || h(0) == h(1) || h(0) == h(2) {
					t.Errorf("hashes %s %s %s", h(0), h(1), h(2))
				}
				// The same bytes hash the same: the first block alone.
				if again := parseOne(t, "variable \"a\" {\n  type = string\n}\n").Variables[0].Hash; again != h(0) {
					t.Errorf("hash %s, want %s", again, h(0))
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) { tc.check(t, parseOne(t, tc.src)) })
	}
}

func TestParseDirUnsupported(t *testing.T) {
	deep := "locals {\n  x = " + strings.Repeat("[", 80) + strings.Repeat("]", 80) + "\n}\n"
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"json":           {map[string]string{"main.tf.json": "{}"}, "main.tf.json is a JSON configuration file"},
		"override":       {map[string]string{"override.tf": ""}, "override.tf is an override file"},
		"suffix":         {map[string]string{"x_override.tf": ""}, "x_override.tf is an override file"},
		"parse error":    {map[string]string{"main.tf": "variable {"}, "main.tf: parse error"},
		"symlink":        {map[string]string{"real.txt": "", "main.tf": "->real.txt"}, "main.tf is a symbolic link"},
		"deep":           {map[string]string{"main.tf": deep}, "main.tf: nesting depth 81 over the limit 64"},
		"other files ok": {map[string]string{"README.md": "x", "sub/bad.tf": "{"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := ParseDir(mustTree(t, writeTree(t, tc.files)), ".", DefaultLimits)
			if got := strings.Join(m.Unsupported, "; "); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A file over a limit gives the limit_exceeded reason code: over the token,
// nesting or unary operator limit when parsed, or over the size limit when
// read.
func TestParseDirLimitExceeded(t *testing.T) {
	const src = "variable \"x\" {\n  type = string\n}\n"
	deep := "locals {\n  x = " + strings.Repeat("[", 65) + strings.Repeat("]", 65) + "\n}\n"
	unary := "variable \"x\" {\n  default = " + strings.Repeat("!", 65) + "true\n}\n"
	for name, tc := range map[string]struct {
		src string
		lim Limits
	}{
		"tokens":          {src: src, lim: func() Limits { l := DefaultLimits; l.MaxTokens = 3; return l }()},
		"file bytes":      {src: src, lim: func() Limits { l := DefaultLimits; l.MaxFileBytes = 8; return l }()},
		"nesting":         {src: deep, lim: DefaultLimits},
		"unary operators": {src: unary, lim: DefaultLimits},
	} {
		t.Run(name, func(t *testing.T) {
			tr, err := ReadTree(writeTree(t, map[string]string{"main.tf": tc.src}), tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			m, err := ParseDir(tr, ".", tc.lim)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.reasonCode(); got != ReasonLimitExceeded {
				t.Errorf("reason code %q, want %q (unsupported %v)", got, ReasonLimitExceeded, m.Unsupported)
			}
		})
	}
}
