package facts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// compatVar parses one variable with the given type text, or none when typ
// is empty.
func compatVar(t *testing.T, typ string) *Variable {
	t.Helper()
	src := "variable \"v\" {\n"
	if typ != "" {
		src += "  type = " + typ + "\n"
	}
	m := parseOne(t, src+"}\n")
	return &m.Variables[0]
}

func TestCompareTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, base, head string
		narrowed         bool
		defaults         []string // paths of changed optional() defaults
	}{
		{name: "same tokens", base: "map( string )", head: "map(string) # same", narrowed: false},
		{name: "head any", base: "list(string)", head: "any", narrowed: false},
		{name: "head untyped", base: "string", head: "", narrowed: false},
		{name: "base any", base: "any", head: "string", narrowed: true},
		{name: "base untyped", base: "", head: "string", narrowed: true},
		{name: "primitive changed", base: "number", head: "string", narrowed: true},
		{name: "any inside a map", base: "map(string)", head: "map(any)", narrowed: true},
		{name: "any inside a list of objects", base: "list(object({a = string}))", head: "list(object({a = any}))", narrowed: true},
		{name: "any as an object attribute", base: "object({a = string})", head: "object({a = any})", narrowed: false},
		{name: "any as a tuple element", base: "tuple([string, number])", head: "tuple([any, number])", narrowed: false},
		{name: "collection kind changed", base: "list(string)", head: "set(string)", narrowed: true},
		{name: "tuple shortened", base: "tuple([string, number])", head: "tuple([string])", narrowed: true},
		{name: "optional attribute added", base: "object({a = string})", head: "object({a = string, b = optional(number)})", narrowed: true},
		{name: "optional attribute with default added", base: "object({a = string})", head: "object({a = string, b = optional(number, 1)})", narrowed: true},
		{name: "required attribute added", base: "object({a = string})", head: "object({a = string, b = number})", narrowed: true},
		{name: "attribute removed", base: "object({a = string, b = number})", head: "object({a = string})", narrowed: true},
		{name: "attribute made optional", base: "object({a = string})", head: "object({a = optional(string)})", narrowed: false},
		{name: "attribute made required", base: "object({a = optional(string)})", head: "object({a = string})", narrowed: true},
		{name: "optional default changed", base: "object({a = optional(number, 1)})", head: "object({a = optional(number, 2)})", defaults: []string{"a"}},
		{name: "optional default added", base: "object({a = optional(number)})", head: "object({a = optional(number, 2)})", defaults: []string{"a"}},
		{name: "optional default dropped", base: "object({a = optional(number, 1)})", head: "object({a = optional(number)})", defaults: []string{"a"}},
		{name: "optional default respaced", base: "object({a = optional(list(string), [\"x\",\"y\"])})", head: "object({a = optional(list(string), [\"x\", \"y\"])})"},
		{name: "nested default changed", base: "list(object({m = optional(object({p = optional(number, 1)}), {})}))", head: "list(object({m = optional(object({p = optional(number, 9)}), {})}))", defaults: []string{"m.p"}},
		{name: "defaults lost to any", base: "object({a = optional(number, 1)})", head: "any", defaults: []string{""}},
		{name: "defaults lost to another type", base: "object({a = optional(number, 1)})", head: "map(number)", narrowed: true, defaults: []string{""}},
		{name: "defaults lost with the type", base: "object({a = optional(number, 1)})", head: "", defaults: []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := compareTypes(compatVar(t, tt.base), compatVar(t, tt.head))
			var paths []string
			for _, x := range d.defaults {
				paths = append(paths, x.path)
			}
			if d.narrowed != tt.narrowed || !slices.Equal(paths, tt.defaults) {
				t.Errorf("narrowed %v defaults %q, want %v %q", d.narrowed, paths, tt.narrowed, tt.defaults)
			}
		})
	}
}

// runCompat runs the compat interface rules on a base and a head tree.
func runCompat(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	r, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits}, compatIfaceRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompatEvidenceCapText(t *testing.T) {
	t.Parallel()
	long := func(c string) string { return `"` + strings.Repeat(c, maxEvidenceText+10) + `"` }
	r := runCompat(t,
		map[string]string{"variables.tf": "variable \"v\" {\n  type    = string\n  default = " + long("a") + "\n}\n"},
		map[string]string{"variables.tf": "variable \"v\" {\n  type    = string\n  default = " + long("b") + "\n}\n"})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.RuleID != "compat.default-changed" || c.EvidenceComplete || len(*c.Evidence.Base) != maxEvidenceText ||
		len(*c.Evidence.Head) != maxEvidenceText || len(c.Evidence.SourceLines[0].Text) != maxEvidenceText {
		t.Errorf("cap did not bite: %+v", c)
	}
}

func TestCompatEvidenceCapLines(t *testing.T) {
	t.Parallel()
	var vs strings.Builder
	for i := range maxEvidenceLines + 1 {
		fmt.Fprintf(&vs, "  validation {\n    condition     = var.v != %q\n    error_message = \"No.\"\n  }\n", fmt.Sprint(i))
	}
	r := runCompat(t,
		map[string]string{"variables.tf": "variable \"v\" {\n  type = string\n}\n"},
		map[string]string{"variables.tf": "variable \"v\" {\n  type = string\n" + vs.String() + "}\n"})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.RuleID != "compat.type-narrowed" || c.EvidenceComplete || len(c.Evidence.SourceLines) != maxEvidenceLines || *c.Line != 3 {
		t.Errorf("cap did not bite: complete %v, %d lines, line %d", c.EvidenceComplete, len(c.Evidence.SourceLines), *c.Line)
	}
}

// A long removed name is capped in the subject and the evidence.
func TestCompatEvidenceCapSubject(t *testing.T) {
	t.Parallel()
	name := strings.Repeat("n", maxEvidenceText+1)
	r := runCompat(t, map[string]string{"variables.tf": "variable \"" + name + "\" {}\n"}, map[string]string{"variables.tf": "\n"})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.EvidenceComplete || c.Subject != ".:"+("var." + name)[:maxEvidenceText] {
		t.Errorf("subject %q complete %v", c.Subject, c.EvidenceComplete)
	}
}

// Of two variables with one name the first counts, so the second gives no
// second candidate with the same subject.
func TestCompatDuplicateNames(t *testing.T) {
	t.Parallel()
	r := runCompat(t,
		map[string]string{"main.tf": "variable \"v\" {}\nvariable \"v\" {}\noutput \"o\" {\n  value = 1\n}\noutput \"o\" {\n  value = 2\n}\n"},
		map[string]string{"main.tf": "\n"})
	var got []string
	for _, c := range r.Candidates {
		got = append(got, c.Subject)
	}
	if !slices.Equal(got, []string{".:var.v", ".:output.o"}) {
		t.Errorf("subjects %q", got)
	}
}

// A removed area removes every variable and output it had.
func TestCompatRemovedSubmodule(t *testing.T) {
	t.Parallel()
	r := runCompat(t,
		map[string]string{"main.tf": "\n", "modules/a/main.tf": "variable \"x\" {}\noutput \"y\" {\n  value = 1\n}\n"},
		map[string]string{"main.tf": "\n"})
	var got []string
	for _, c := range r.Candidates {
		got = append(got, c.RuleID+" "+c.File+" "+c.Subject)
	}
	want := []string{"compat.variable-removed modules/a/main.tf modules/a:var.x", "compat.variable-removed modules/a/main.tf modules/a:output.y"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
}

// A new input with a non-null default is a default-changed candidate only
// where configuration that exists at the base reads it at the head: count,
// for_each or an argument of a block present at the base, or the value of
// an output present at the base, directly or through locals. A new
// argument, a new block or no read at all is no candidate, nor is a null
// default or a new module. A base directory with no block is a new module.
// A read of a new variable that narrowing could hide must keep the
// candidate: it goes through a whole-object or indexed reference, a for
// expression, a dynamic block, a local in another file, a block moved or
// inserted, a provider, a module call or an existing validation. A read
// that no configuration of the base reaches, such as one only an example has
// or one in a block of a new type, stays none.
func TestCompatNewVariableDefaultFailClosed(t *testing.T) {
	t.Parallel()
	const r = "resource \"aws_r\" \"x\" {\n"
	vars := "variable \"a\" {\n  default = \"x\"\n}\n"
	const want = "compat.default-changed modules/m:var.a"
	tests := []struct {
		name       string
		base, head map[string]string // files of modules/m; head variables.tf defaults to a new variable a
		extra      map[string]string // other head files, by repository path
		found      bool
	}{
		{name: "whole object", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = length(var)\n}\n"}, found: true},
		{name: "whole object in a for expression", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = join(\",\", [for k, v in var : k])\n}\n"}, found: true},
		{name: "string index", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = var[\"a\"]\n}\n"}, found: true},
		{name: "unresolved index", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = var[local.k]\n}\n", "locals.tf": "locals {\n  k = \"a\"\n}\n"}, found: true},
		{name: "for expression over the variable", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = join(\",\", [for s in var.a : s])\n}\n"}, found: true},
		{name: "local by string index", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = local[\"m\"]\n}\n", "locals.tf": "locals {\n  m = var.a\n}\n"}, found: true},
		{name: "whole locals object", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = length(local)\n}\n", "locals.tf": "locals {\n  m = var.a\n}\n"}, found: true},
		{name: "local in another file", base: map[string]string{"main.tf": r + "  name = local.n\n}\n", "locals.tf": "locals {\n  n = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = local.n\n}\n", "locals.tf": "locals {\n  n = var.a\n}\n"}, found: true},
		{name: "dynamic block for_each", base: map[string]string{"main.tf": r + "  dynamic \"t\" {\n    for_each = []\n    content {\n      v = \"f\"\n    }\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  dynamic \"t\" {\n    for_each = var.a\n    content {\n      v = \"f\"\n    }\n  }\n}\n"}, found: true},
		{name: "dynamic block content", base: map[string]string{"main.tf": r + "  dynamic \"t\" {\n    for_each = []\n    content {\n      v = \"f\"\n    }\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  dynamic \"t\" {\n    for_each = []\n    content {\n      v = var.a\n    }\n  }\n}\n"}, found: true},
		{name: "static block made dynamic", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  dynamic \"t\" {\n    for_each = var.a\n    content {\n      v = \"f\"\n    }\n  }\n}\n"}, found: true},
		{name: "block inserted before the existing one", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    w = \"n\"\n  }\n  t {\n    v = var.a\n  }\n}\n"}, found: true},
		{name: "block of a new type", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n  u {\n    v = var.a\n  }\n}\n"}},
		{name: "resource renamed with a moved block", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_r\" \"y\" {\n  name = var.a\n}\nmoved {\n  from = aws_r.x\n  to   = aws_r.y\n}\n"}, found: true},
		{name: "resource renamed without a moved block", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_r\" \"y\" {\n  name = var.a\n}\n"}},
		{name: "provider argument", base: map[string]string{"main.tf": "provider \"aws\" {\n  region = \"eu\"\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  region = var.a\n}\n"}, found: true},
		{name: "module call argument", base: map[string]string{"main.tf": "module \"c\" {\n  source = \"./c\"\n  name   = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "module \"c\" {\n  source = \"./c\"\n  name   = var.a\n}\n"}, found: true},
		{name: "module call count", base: map[string]string{"main.tf": "module \"c\" {\n  source = \"./c\"\n}\n"},
			head: map[string]string{"main.tf": "module \"c\" {\n  source = \"./c\"\n  count  = var.a ? 1 : 0\n}\n"}, found: true},
		{name: "output precondition", base: map[string]string{"main.tf": "output \"o\" {\n  value = 1\n  precondition {\n    condition     = true\n    error_message = \"e\"\n  }\n}\n"},
			head: map[string]string{"main.tf": "output \"o\" {\n  value = 1\n  precondition {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "existing validation", base: map[string]string{"main.tf": "\n", "variables.tf": "variable \"c\" {\n  validation {\n    condition     = true\n    error_message = \"e\"\n  }\n}\n"},
			head: map[string]string{"main.tf": "\n", "variables.tf": vars + "variable \"c\" {\n  validation {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "import inserted before the existing one", base: map[string]string{"main.tf": "import {\n  to = aws_r.x\n  id = \"i\"\n}\n"},
			head: map[string]string{"main.tf": "import {\n  to = aws_r.y\n  id = \"j\"\n}\nimport {\n  to = aws_r.x\n  id = var.a\n}\n"}, found: true},
		{name: "import rewritten in place", base: map[string]string{"main.tf": "import {\n  to = aws_r.x\n  id = \"i\"\n}\n"},
			head: map[string]string{"main.tf": "import {\n  to = aws_r.x\n  id = var.a\n}\n"}, found: true},
		{name: "blocks of one type swapped", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n  t {\n    w = \"g\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    w = \"g\"\n  }\n  t {\n    v = var.a\n  }\n}\n"}, found: true},
		{name: "nested blocks swapped", base: map[string]string{"main.tf": r + "  t {\n    u {\n      v = \"f\"\n    }\n    u {\n      w = \"g\"\n    }\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    u {\n      w = \"g\"\n    }\n    u {\n      v = var.a\n    }\n  }\n}\n"}, found: true},
		{name: "new lifecycle precondition", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n  lifecycle {\n    precondition {\n      condition     = var.a != \"\"\n      error_message = \"e\"\n    }\n  }\n}\n"}, found: true},
		{name: "new precondition in an existing lifecycle", base: map[string]string{"main.tf": r + "  lifecycle {\n    prevent_destroy = true\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  lifecycle {\n    prevent_destroy = true\n    postcondition {\n      condition     = var.a != \"\"\n      error_message = \"e\"\n    }\n  }\n}\n"}, found: true},
		{name: "new assert in an existing check", base: map[string]string{"main.tf": "check \"c\" {\n  assert {\n    condition     = true\n    error_message = \"e\"\n  }\n}\n"},
			head: map[string]string{"main.tf": "check \"c\" {\n  assert {\n    condition     = true\n    error_message = \"e\"\n  }\n  assert {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new precondition in an existing output", base: map[string]string{"main.tf": "output \"o\" {\n  value = 1\n}\n"},
			head: map[string]string{"main.tf": "output \"o\" {\n  value = 1\n  precondition {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new check block", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\ncheck \"c\" {\n  assert {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}},
		{name: "new resource with a precondition", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\nresource \"aws_r\" \"y\" {\n  lifecycle {\n    precondition {\n      condition     = var.a != \"\"\n      error_message = \"e\"\n    }\n  }\n}\n"}},
		{name: "second block of an existing type", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n  t {\n    w = var.a\n  }\n}\n"}, found: true},
		{name: "new type inside an ambiguous block", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n  t {\n    v = \"f\"\n    u {\n      w = var.a\n    }\n  }\n}\n"}, found: true},
		{name: "new dynamic block of a new type", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n  dynamic \"u\" {\n    for_each = var.a\n    content {\n      v = \"f\"\n    }\n  }\n}\n"}},
		{name: "data block beside a resource of the same name", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\ndata \"aws_r\" \"x\" {\n  name = var.a\n}\n"}},
		{name: "data block argument", base: map[string]string{"main.tf": "data \"aws_r\" \"x\" {\n  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "data \"aws_r\" \"x\" {\n  name = var.a\n}\n"}, found: true},
		{name: "for_each added", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  for_each = var.a\n  name     = \"f\"\n}\n"}, found: true},
		{name: "count named argument of a nested block", base: map[string]string{"main.tf": r + "  t {\n    v = \"f\"\n  }\n}\n"},
			head: map[string]string{"main.tf": r + "  t {\n    v     = \"f\"\n    count = var.a\n  }\n}\n"}},
		{name: "validation added to an existing variable", base: map[string]string{"main.tf": "\n", "variables.tf": "variable \"c\" {}\n"},
			head: map[string]string{"main.tf": "\n", "variables.tf": vars + "variable \"c\" {\n  validation {\n    condition     = var.a != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "validation with no message", base: map[string]string{"main.tf": "\n", "variables.tf": "variable \"c\" {\n  validation {\n    condition = true\n  }\n}\n"},
			head: map[string]string{"main.tf": "\n", "variables.tf": vars + "variable \"c\" {\n  validation {\n    condition = true\n  }\n}\n"}},
		{name: "new resource read by an existing argument", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = try(aws_k.this[0].arn, \"f\")\n}\nresource \"aws_k\" \"this\" {\n  count = var.a ? 1 : 0\n}\n"}, found: true},
		{name: "new resource read through a new data source", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = aws_k.this.arn\n}\nresource \"aws_k\" \"this\" {\n  doc = data.aws_d.d.json\n}\ndata \"aws_d\" \"d\" {\n  name = var.a\n}\n"}, found: true},
		{name: "existing output reads a new resource", base: map[string]string{"main.tf": "output \"o\" {\n  value = 1\n}\n"},
			head: map[string]string{"main.tf": "output \"o\" {\n  value = aws_k.this.arn\n}\nresource \"aws_k\" \"this\" {\n  count = var.a ? 1 : 0\n}\n"}, found: true},
		{name: "new module call read by an existing argument", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = module.m.out\n}\nmodule \"m\" {\n  source  = \"./m\"\n  enabled = var.a\n}\n"}, found: true},
		{name: "new resource read that reads no new variable", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = aws_k.this.arn\n}\nresource \"aws_k\" \"this\" {\n  name = \"k\"\n}\n"}},
		{name: "new resources reading each other", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = aws_k.p.arn\n}\nresource \"aws_k\" \"p\" {\n  name = aws_k.q.arn\n}\nresource \"aws_k\" \"q\" {\n  name = aws_k.p.arn\n}\n"}},
		{name: "moved twice", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_r\" \"z\" {\n  name = var.a\n}\nmoved {\n  from = aws_r.x\n  to   = aws_r.y\n}\nmoved {\n  from = aws_r.y\n  to   = aws_r.z\n}\n"}, found: true},
		{name: "two moves into one address", base: map[string]string{"main.tf": "resource \"aws_r\" \"x\" {\n  name = \"f\"\n}\nresource \"aws_r\" \"w\" {\n  other = \"g\"\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_r\" \"z\" {\n  name = var.a\n}\nmoved {\n  from = aws_r.x\n  to   = aws_r.z\n}\nmoved {\n  from = aws_r.w\n  to   = aws_r.z\n}\n"}, found: true},
		{name: "moved cycle", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\nmoved {\n  from = aws_r.x\n  to   = aws_r.y\n}\nmoved {\n  from = aws_r.y\n  to   = aws_r.x\n}\n"}},
		{name: "new variable validation reads another variable", base: map[string]string{"main.tf": "\n", "variables.tf": "variable \"mode\" {}\n"},
			head: map[string]string{"main.tf": "\n", "variables.tf": "variable \"mode\" {}\nvariable \"a\" {\n  default = \"x\"\n  validation {\n    condition     = var.mode == \"a\" ? var.a != \"\" : true\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new variable validation reads a null default's neighbour", base: map[string]string{"main.tf": "\n", "variables.tf": "variable \"mode\" {}\n"},
			head: map[string]string{"main.tf": "\n", "variables.tf": "variable \"mode\" {}\nvariable \"a\" {\n  default = null\n  validation {\n    condition     = var.mode == \"a\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new variable validation rejects its own default", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\n", "variables.tf": "variable \"a\" {\n  default = \"x\"\n  validation {\n    condition     = var.a != \"x\"\n    error_message = \"e ${var.a}\"\n  }\n}\n"}, found: true},
		{name: "imports swapped with for_each rewritten", base: map[string]string{"main.tf": "import {\n  to = aws_r.x\n  id = \"i\"\n}\nimport {\n  to       = aws_r.y\n  id       = \"j\"\n  for_each = local.m\n}\nlocals {\n  m = []\n}\n"},
			head: map[string]string{"main.tf": "import {\n  to       = aws_r.y\n  id       = \"j\"\n  for_each = toset([var.a])\n}\nimport {\n  to = aws_r.x\n  id = \"i\"\n}\nlocals {\n  m = []\n}\n"}, found: true},
		{name: "import gains for_each", base: map[string]string{"main.tf": "import {\n  to = aws_r.x\n  id = \"i\"\n}\n"},
			head: map[string]string{"main.tf": "import {\n  to       = aws_r.x\n  id       = \"i\"\n  for_each = toset([var.a])\n}\n"}, found: true},
		{name: "new variable validation reads the whole var object", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\n", "variables.tf": "variable \"a\" {\n  default = null\n  validation {\n    condition     = length(var) > 0\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new variable validation reads the whole local object", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\n", "variables.tf": "variable \"a\" {\n  default = null\n  validation {\n    condition     = length(local) > 0\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new variable validation reads a local", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\n", "variables.tf": "variable \"a\" {\n  default = null\n  validation {\n    condition     = local.m != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new variable validation reads a data source", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\n", "variables.tf": "variable \"a\" {\n  default = null\n  validation {\n    condition     = data.aws_d.d.id != \"\"\n    error_message = \"e\"\n  }\n}\n"}, found: true},
		{name: "new module with a validation that reads another variable", base: map[string]string{},
			head: map[string]string{"main.tf": "\n", "variables.tf": "variable \"mode\" {}\nvariable \"a\" {\n  default = \"x\"\n  validation {\n    condition     = var.mode == \"a\"\n    error_message = \"e\"\n  }\n}\n"}},
		{name: "provider of a resource switched to a new alias", base: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"o\"\n  region = \"eu\"\n}\nresource \"aws_r\" \"x\" {\n  provider = aws.o\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"o\"\n  region = \"eu\"\n}\nprovider \"aws\" {\n  alias  = \"n\"\n  region = var.a ? \"us\" : \"eu\"\n}\nresource \"aws_r\" \"x\" {\n  provider = aws.n\n}\n"}, found: true},
		{name: "provider meta-argument added to a resource", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"n\"\n  region = var.a ? \"us\" : \"eu\"\n}\n" + r + "  provider = aws.n\n  name     = \"f\"\n}\n"}, found: true},
		{name: "providers of a module call switched to a new alias", base: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"o\"\n  region = \"eu\"\n}\nmodule \"c\" {\n  source    = \"./c\"\n  providers = { aws = aws.o }\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"o\"\n  region = \"eu\"\n}\nprovider \"aws\" {\n  alias  = \"n\"\n  region = var.a ? \"us\" : \"eu\"\n}\nmodule \"c\" {\n  source    = \"./c\"\n  providers = { aws = aws.n }\n}\n"}, found: true},
		{name: "new default provider", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  region = var.a ? \"us\" : \"eu\"\n}\n" + r + "  name = \"f\"\n}\n"}, found: true},
		{name: "new aliased provider nothing uses", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": "provider \"aws\" {\n  alias  = \"n\"\n  region = var.a ? \"us\" : \"eu\"\n}\n" + r + "  name = \"f\"\n}\n"}},
		{name: "new ephemeral resource read by an existing argument", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = ephemeral.aws_e.n.v\n}\nephemeral \"aws_e\" \"n\" {\n  on = var.a\n}\n"}, found: true},
		{name: "new ephemeral resource nothing reads", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head: map[string]string{"main.tf": r + "  name = \"f\"\n}\nephemeral \"aws_e\" \"n\" {\n  on = var.a\n}\n"}},
		{name: "unrelated resource and example only", base: map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			head:  map[string]string{"main.tf": r + "  name = \"f\"\n}\n"},
			extra: map[string]string{"examples/basic/main.tf": "resource \"aws_r\" \"e\" {\n  name = var.a\n}\n", "examples/basic/variables.tf": vars}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := map[string]string{"main.tf": "\n"}
			for k, v := range tt.base {
				base["modules/m/"+k] = v
			}
			head := map[string]string{"main.tf": "\n", "modules/m/variables.tf": vars}
			for k, v := range tt.head {
				head["modules/m/"+k] = v
			}
			maps.Copy(head, tt.extra)
			var got []string
			for _, c := range runCompat(t, base, head).Candidates {
				if c.RuleID == "compat.default-changed" {
					got = append(got, c.RuleID+" "+c.Subject)
				}
			}
			if (len(got) == 1 && got[0] == want) != tt.found || len(got) > 1 {
				t.Errorf("got %q, found want %v", got, tt.found)
			}
		})
	}
}

// varReads reads names from a static key and everything from a whole object,
// a key it cannot resolve, a number key or text that does not parse.
func TestVarReads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text       string
		vars, locs []string
		refs       []string
		allV, allL bool
	}{
		{text: "try(aws_k.this[0].arn, var.a)", vars: []string{"a"}, refs: []string{"aws_k.this"}},
		{text: "data.aws_d.d.id", refs: []string{"data.aws_d.d"}},
		{text: "module.m.out", refs: []string{"module.m"}},
		{text: "ephemeral.aws_e.n.v", refs: []string{"ephemeral.aws_e.n"}},
		{text: "ephemeral.aws_e", refs: nil},
		{text: `ephemeral.aws_e["k"].v`, refs: nil},
		{text: `ephemeral["x"].n.v`, refs: nil},
		{text: "resource.aws_k.this.arn", refs: []string{"aws_k.this"}},
		{text: "var.a", vars: []string{"a"}},
		{text: `var["a"]`, vars: []string{"a"}},
		{text: "local.m", locs: []string{"m"}},
		{text: `local["m"]`, locs: []string{"m"}},
		{text: "var[0]", allV: true},
		{text: "var[local.k]", allV: true, locs: []string{"k"}},
		{text: "length(var)", allV: true},
		{text: "length(local)", allL: true},
		{text: "each.value", vars: nil},
		{text: "(", allV: true, allL: true},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()
			r := newVarReads()
			r.add(&Expr{Text: tt.text})
			r.add(nil)
			got := func(m map[string]bool) []string {
				var s []string
				for k := range m {
					s = append(s, k)
				}
				slices.Sort(s)
				return s
			}
			if !slices.Equal(got(r.vars), tt.vars) || !slices.Equal(got(r.locals), tt.locs) || !slices.Equal(got(r.refs), tt.refs) ||
				r.allVars != tt.allV || r.allLocals != tt.allL {
				t.Errorf("vars %v locals %v allVars %v allLocals %v", got(r.vars), got(r.locals), r.allVars, r.allLocals)
			}
		})
	}
}

func TestCompatNewVariableDefault(t *testing.T) {
	t.Parallel()
	vars := "variable \"a\" {\n  default = \"x\"\n}\nvariable \"b\" {\n  default = null\n}\n"
	res := func(body string) string { return "resource \"aws_r\" \"x\" {\n  name = " + body + "\n}\n" }
	base := map[string]string{"main.tf": "\n", "modules/m/main.tf": res(`"fixed"`) + "output \"o\" {\n  value = \"v\"\n}\nlocals {\n  n = \"fixed\"\n}\n"}
	want := []string{"compat.default-changed modules/m:var.a"}
	tests := []struct {
		name string
		base map[string]string
		head string
		want []string
	}{
		{name: "unread", base: base, head: res(`"fixed"`)},
		{name: "new argument", base: base, head: "resource \"aws_r\" \"x\" {\n  name = \"fixed\"\n  mode = var.a\n}\n"},
		{name: "new block", base: base, head: res(`"fixed"`) + "resource \"aws_r\" \"y\" {\n  count = var.a ? 1 : 0\n  name  = var.b\n}\n"},
		{name: "argument rewritten", base: base, head: res("var.a"), want: want},
		{name: "argument through a new local", base: base, head: res("local.m") + "locals {\n  m = var.a\n}\n", want: want},
		{name: "local rewritten", base: map[string]string{"main.tf": "\n", "modules/m/main.tf": res("local.n") + "locals {\n  n = \"fixed\"\n}\n"},
			head: res("local.n") + "locals {\n  n = var.a\n}\n", want: want},
		{name: "count added", base: base, head: "resource \"aws_r\" \"x\" {\n  count = var.a ? 1 : 0\n  name  = \"fixed\"\n}\n", want: want},
		{name: "output", base: base, head: res(`"fixed"`) + "output \"o\" {\n  value = var.a\n}\n", want: want},
		{name: "null default read", base: base, head: res("var.b")},
		{name: "base without blocks", base: map[string]string{"main.tf": "\n", "modules/m/README.md": "x\n"}, head: res("var.a")},
		{name: "new module", base: map[string]string{"main.tf": "\n"}, head: res("var.a")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := runCompat(t, tt.base, map[string]string{"main.tf": "\n", "modules/m/main.tf": tt.head, "modules/m/variables.tf": vars})
			var got []string
			for _, c := range r.Candidates {
				if c.RuleID == "compat.default-changed" {
					got = append(got, c.RuleID+" "+c.Subject)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
