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

func TestParseDir(t *testing.T) {
	m, _ := ParseDir(mustTree(t, writeTree(t, map[string]string{"main.tf": parseSrc})), ".", DefaultLimits)
	if len(m.Unsupported) > 0 {
		t.Fatal(m.Unsupported)
	}
	wantVars := []Variable{
		{Name: "typed", File: "main.tf", Line: 11, HasType: true, TypeText: "string", HasDescription: true},
		{Name: "loose", File: "main.tf", Line: 17, HasType: true, TypeText: "any", TypeUsesAny: true, Comments: []string{"# Accepts a list or a map."}},
		{Name: "nested", File: "main.tf", Line: 21, HasType: true, TypeText: "map(object({ x = optional(any) }))", TypeUsesAny: true},
	}
	if !reflect.DeepEqual(m.Variables, wantVars) {
		t.Errorf("variables\n got %+v\nwant %+v", m.Variables, wantVars)
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
