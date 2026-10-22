package facts

import (
	"fmt"
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

// A new input with a non-null default is a default-changed candidate in a
// module that exists at the base, and clean in a new module or with a null
// default. A base directory with no block is a new module.
func TestCompatNewVariableDefault(t *testing.T) {
	t.Parallel()
	head := "variable \"a\" {\n  default = \"x\"\n}\nvariable \"b\" {\n  default = null\n}\n"
	tests := []struct {
		name string
		base map[string]string
		want []string
	}{
		{name: "existing module", base: map[string]string{"main.tf": "\n", "modules/m/main.tf": "locals {}\n"}, want: []string{"compat.default-changed modules/m:var.a"}},
		{name: "base without blocks", base: map[string]string{"main.tf": "\n", "modules/m/README.md": "x\n"}},
		{name: "new module", base: map[string]string{"main.tf": "\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := runCompat(t, tt.base, map[string]string{"main.tf": "\n", "modules/m/main.tf": head})
			var got []string
			for _, c := range r.Candidates {
				got = append(got, c.RuleID+" "+c.Subject)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
