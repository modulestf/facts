package behaviour

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// declSchema has aws_thing with an optional attribute "opt", a required
// attribute "req", a block "blk" with min_items 1 holding a required "inner"
// and an optional "loose", a block "free" with no min_items, and a nested
// attribute type "obj" with a required attribute "must".
const declSchema = `{"format_version":"1.0","provider_schemas":{
"registry.terraform.io/hashicorp/aws":{
  "resource_schemas":{"aws_thing":{"version":0,"block":{
    "attributes":{
      "opt":{"type":"string","optional":true},
      "req":{"type":"string","required":true},
      "obj":{"nested_type":{"nesting_mode":"single","attributes":{"must":{"type":"string","required":true}}},"optional":true}},
    "block_types":{
      "blk":{"nesting_mode":"list","min_items":1,"block":{"attributes":{
        "inner":{"type":"string","required":true},
        "loose":{"type":"string","optional":true}}}},
      "free":{"nesting_mode":"list","block":{"attributes":{"x":{"type":"string","optional":true}}}}}}}},
  "data_source_schemas":{"aws_look":{"version":0,"block":{"attributes":{
    "name":{"type":"string","optional":true},
    "key":{"type":"string","required":true}}}}}}}}`

// optFromX is a resource whose added argument opt reads only var.x.
const optFromX = `resource "aws_thing" "t" {
  opt = var.x
}
`

// declOf returns the declaration of input name on the trigger at address
// addr and attribute attr, or nil.
func declOf(t *testing.T, r *Report, addr, attr, name string) *Declaration {
	t.Helper()
	for _, tr := range atAttr(r, addr, attr) {
		for _, in := range tr.ReferencedInputs {
			if in.Name == name {
				return in.Declaration
			}
		}
	}
	return nil
}

// varDecl returns the declaration on the trigger of var.<name> in file.
func varDecl(r *Report, file, name string) (*Declaration, bool) {
	for _, tr := range r.Triggers {
		if tr.File == file && str(tr.Address) == "var."+name {
			return tr.Declaration, true
		}
	}
	return nil, false
}

func declString(d *Declaration) string {
	if d == nil {
		return "<none>"
	}
	added := "existing"
	if d.Added {
		added = "added"
	}
	return added + " default=" + d.Default + " nullable=" + d.Nullable
}

// TestDeclarationOracle holds the declaration facts of a variable the
// change adds, one case per parsing case of the shell reader this replaces
// (comments, heredocs, nested types, escaped quotes, duplicates, sizes).
// The parser reads HCL as Terraform does, so a case the line reader refused
// as unreadable now has its real facts; the want column says which.
func TestDeclarationOracle(t *testing.T) {
	cases := []struct {
		name, block, want string
	}{
		{"null default", "variable \"x\" {\n  type    = string\n  default = null\n}\n", "added default=null nullable=unset"},
		{"default true", "variable \"x\" {\n  type    = bool\n  default = true\n}\n", "added default=literal nullable=unset"},
		{"no default", "variable \"x\" {\n  type = string\n}\n", "added default=none nullable=unset"},
		{"nullable line", "variable \"x\" {\n  type     = string\n  default  = null\n  nullable = true\n}\n", "added default=null nullable=true"},
		{"nullable false", "variable \"x\" {\n  type     = string\n  default  = null\n  nullable = false\n}\n", "added default=null nullable=false"},
		{"nullable constant expression", "variable \"x\" {\n  default  = null\n  nullable = !false\n}\n", "added default=null nullable=true"},
		{"nullable string", "variable \"x\" {\n  default  = null\n  nullable = \"yes\"\n}\n", "added default=null nullable=other"},
		{"nullable null", "variable \"x\" {\n  default  = null\n  nullable = null\n}\n", "added default=null nullable=other"},
		{"for expression default", "variable \"x\" {\n  default = [for v in [null] : v]\n}\n", "added default=other nullable=unset"},
		{"splat default", "variable \"x\" {\n  default = [null][*]\n}\n", "added default=other nullable=unset"},
		{"nullable reference", "variable \"x\" {\n  default  = null\n  nullable = local.n\n}\n", "added default=null nullable=other"},
		// The line reader refused a heredoc; the parser reads the default.
		{"heredoc description", "variable \"x\" {\n  description = <<-EOT\n    default = 1\n  EOT\n  default = null\n}\n", "added default=null nullable=unset"},
		{"heredoc default", "variable \"x\" {\n  default = <<-EOT\n    null\n  EOT\n}\n", "added default=literal nullable=unset"},
		// Comments: a default = null only in a comment is no default.
		{"default behind a block comment", "variable \"x\" {\n  /* default = null */\n  default = 1\n}\n", "added default=literal nullable=unset"},
		{"block comment beside a real default", "variable \"x\" {\n  default = 2 /* default = null */\n}\n", "added default=literal nullable=unset"},
		{"closing brace in a block comment", "variable \"x\" {\n  /* } */\n  default = null\n}\n", "added default=null nullable=unset"},
		{"hash comment", "variable \"x\" {\n  # default = 1\n  default = null\n}\n", "added default=null nullable=unset"},
		{"slash comment", "variable \"x\" {\n  default = null // was 1\n}\n", "added default=null nullable=unset"},
		{"only a commented default", "variable \"x\" {\n  # default = null\n}\n", "added default=none nullable=unset"},
		{"default inside an object type", "variable \"x\" {\n  type = object({\n    a = optional(string, null)\n  })\n  default = null\n}\n", "added default=null nullable=unset"},
		{"description holding default", "variable \"x\" {\n  description = \"default = 1, see http://x/#a \\\"q\\\"\"\n  default     = null\n}\n", "added default=null nullable=unset"},
		{"escaped quote before a brace", "variable \"x\" {\n  description = \"a \\\"}\\\" b\"\n  default     = null\n}\n", "added default=null nullable=unset"},
		{"string ending in an escaped backslash", "variable \"x\" {\n  description = \"a\\\\\"\n  default     = null\n}\n", "added default=null nullable=unset"},
		{"escaped quotes around brackets", "variable \"x\" {\n  description = \"\\\"[\\\"\"\n  default     = 3000\n}\n", "added default=literal nullable=unset"},
		{"null inside a nested object", "variable \"x\" {\n  default = {\n    a = null\n  }\n}\n", "added default=literal nullable=unset"},
		{"null inside a list", "variable \"x\" {\n  default = [null]\n}\n", "added default=literal nullable=unset"},
		{"null inside a call", "variable \"x\" {\n  default = tolist([null])\n}\n", "added default=other nullable=unset"},
		{"column-0 brace of a nested type", "variable \"x\" {\n  type = object({\n    a = string\n})\n  default = null\n}\n", "added default=null nullable=unset"},
		{"parenthesised null", "variable \"x\" {\n  default = (null)\n}\n", "added default=null nullable=unset"},
		{"literal template", "variable \"x\" {\n  default = \"a${\"b\"}\"\n}\n", "added default=literal nullable=unset"},
		{"template directive", "variable \"x\" {\n  default = \"%{if true}a%{endif}\"\n}\n", "added default=literal nullable=unset"},
		{"reference default", "variable \"x\" {\n  default = local.y\n}\n", "added default=other nullable=unset"},
		{"name holding default", "variable \"x\" {\n  default = null\n}\nvariable \"default_x\" {\n  default = 1\n}\n", "added default=null nullable=unset"},
		{"long block", "variable \"x\" {\n" + strings.Repeat("  # line\n", 250) + "  default = null\n}\n", "added default=null nullable=unset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, map[string]string{"main.tf": `resource "aws_thing" "t" {}` + "\n"},
				map[string]string{"main.tf": optFromX, "variables.tf": c.block}, "")
			if got := declString(declOf(t, r, "aws_thing.t", "opt", "x")); got != c.want {
				t.Errorf("input: want %s, got %s", c.want, got)
			}
			if d, ok := varDecl(r, "variables.tf", "x"); !ok || declString(d) != c.want {
				t.Errorf("var.x trigger (found %v): want %s, got %s", ok, c.want, declString(d))
			}
		})
	}
}

// TestDeclarationUnreadable: a directory that does not decompose (two
// defaults, a string left open or an interpolation outside a string, which
// do not parse, a link) gives no declaration; neither does a removed
// variable or one declared elsewhere.
func TestDeclarationUnreadable(t *testing.T) {
	base := map[string]string{"main.tf": `resource "aws_thing" "t" {}` + "\n"}
	for name, block := range map[string]string{
		"two defaults":     "variable \"x\" {\n  default = null\n  default = 1\n}\n",
		"string left open": "variable \"x\" {\n  default = \"a\n}\n",
		"bare interp":      "variable \"x\" {\n  default = ${null}\n}\n",
	} {
		r := runPair(t, base, map[string]string{"main.tf": optFromX, "variables.tf": block}, "")
		if d := declOf(t, r, "aws_thing.t", "opt", "x"); d != nil {
			t.Errorf("%s: want no declaration, got %s", name, declString(d))
		}
	}
	// The variables file is a link.
	headDir := writeTree(t, map[string]string{"main.tf": optFromX, "real.tf.txt": "variable \"x\" {\n  default = null\n}\n"})
	if err := os.Symlink("real.tf.txt", filepath.Join(headDir, "variables.tf")); err != nil {
		t.Fatal(err)
	}
	r, err := Run(Options{Head: headDir, Base: writeTree(t, base), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if d := declOf(t, r, "aws_thing.t", "opt", "x"); d != nil {
		t.Errorf("link: want no declaration, got %s", declString(d))
	}
	// A removed variable has no head declaration.
	r = runPair(t, map[string]string{"main.tf": optFromX, "variables.tf": varX}, map[string]string{"main.tf": `resource "aws_thing" "t" {}` + "\n"}, "")
	if d, ok := varDecl(r, "variables.tf", "x"); !ok || d != nil {
		t.Errorf("removed: want the trigger with no declaration, found %v got %s", ok, declString(d))
	}
}

// TestDeclarationAddedAndScope: added is per module directory. A variable
// of the base is existing; the same name added in a submodule is added
// there with its own default, while the root's keeps its own.
func TestDeclarationAddedAndScope(t *testing.T) {
	base := map[string]string{
		"main.tf":           `resource "aws_thing" "t" {}` + "\n",
		"variables.tf":      "variable \"x\" {\n  default = true\n}\n",
		"modules/s/main.tf": `resource "aws_thing" "t" {}` + "\n",
	}
	head := map[string]string{
		"main.tf":                optFromX,
		"variables.tf":           "variable \"x\" {\n  default = true\n}\n",
		"modules/s/main.tf":      optFromX,
		"modules/s/variables.tf": varX,
	}
	r := runPair(t, base, head, "")
	var root, sub *Declaration
	for _, tr := range atAttr(r, "aws_thing.t", "opt") {
		for _, in := range tr.ReferencedInputs {
			switch tr.File {
			case "main.tf":
				root = in.Declaration
			case "modules/s/main.tf":
				sub = in.Declaration
			}
		}
	}
	if got := declString(root); got != "existing default=literal nullable=unset" {
		t.Errorf("root: %s", got)
	}
	if got := declString(sub); got != "added default=null nullable=unset" {
		t.Errorf("submodule: %s", got)
	}
	// A whole new directory: every variable is added.
	r = runPair(t, map[string]string{"main.tf": "\n"}, map[string]string{"main.tf": "\n", "modules/n/main.tf": optFromX + varX}, "")
	if d, ok := varDecl(r, "modules/n/main.tf", "x"); !ok || declString(d) != "added default=null nullable=unset" {
		t.Errorf("new directory (found %v): %s", ok, declString(d))
	}
}

// TestSchemaRequired: with --schema, a resource or data source argument or
// nested block trigger says whether the schema requires it at that path,
// at any block depth, or holds a block with min_items above 0. A type,
// provider or path the schema lacks is unknown; no schema, no field.
func TestSchemaRequired(t *testing.T) {
	sch := writeJSON(t, "schema.json", declSchema)
	base := map[string]string{"main.tf": varX + `resource "aws_thing" "t" {
  blk {
    inner = "a"
  }
}
data "aws_look" "l" {}
resource "aws_other" "o" {}
`}
	head := map[string]string{"main.tf": varX + `resource "aws_thing" "t" {
  opt  = var.x
  req  = var.x
  nope = var.x
  obj = {
    must = var.x
  }
  blk {
    inner = "a"
    loose = var.x
  }
  free {
    x = "b"
  }
  dynamic "blk" {
    for_each = var.x == null ? [] : [1]
    content {
      inner = "c"
    }
  }
}
data "aws_look" "l" {
  name = var.x
  key  = var.x
}
resource "aws_other" "o" {
  a = var.x
}
`}
	r := runPair(t, base, head, sch)
	want := map[[2]string]string{
		{"aws_thing.t", "opt"}:         "not_required",
		{"aws_thing.t", "req"}:         "required",
		{"aws_thing.t", "nope"}:        "unknown",
		{"aws_thing.t", "obj"}:         "not_required",
		{"aws_thing.t", "blk.loose"}:   "not_required",
		{"aws_thing.t", "free"}:        "not_required",
		{"aws_thing.t", "dynamic.blk"}: "required",
		{"data.aws_look.l", "name"}:    "not_required",
		{"data.aws_look.l", "key"}:     "required",
		{"aws_other.o", "a"}:           "unknown",
	}
	for k, w := range want {
		trs := atAttr(r, k[0], k[1])
		if len(trs) == 0 {
			t.Errorf("%s %s: no trigger: %v", k[0], k[1], categories(r))
			continue
		}
		for _, tr := range trs {
			if tr.SchemaRequired != w {
				t.Errorf("%s %s (%s): want %s, got %q", k[0], k[1], tr.Category, w, tr.SchemaRequired)
			}
		}
	}
	// A required attribute inside a nested block, the block itself kept.
	r = runPair(t, map[string]string{"main.tf": `resource "aws_thing" "t" {
  blk {
    loose = "a"
  }
}
`}, map[string]string{"main.tf": varX + `resource "aws_thing" "t" {
  blk {
    loose = "a"
    inner = var.x
  }
}
`}, sch)
	for _, tr := range atAttr(r, "aws_thing.t", "blk.inner") {
		if tr.SchemaRequired != "required" {
			t.Errorf("blk.inner: %q", tr.SchemaRequired)
		}
	}
	// No schema: no field on any trigger; a variable trigger never has one.
	r = runPair(t, base, head, "")
	for _, tr := range r.Triggers {
		if tr.SchemaRequired != "" {
			t.Errorf("no schema: %s %s has %q", str(tr.Address), str(tr.Attribute), tr.SchemaRequired)
		}
	}
	r = runPair(t, base, head, sch)
	for _, tr := range r.Triggers {
		if strings.HasPrefix(str(tr.Address), "var.") && tr.SchemaRequired != "" {
			t.Errorf("variable trigger has %q", tr.SchemaRequired)
		}
	}
}
