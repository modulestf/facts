package facts

import (
	"fmt"
	"testing"
)

// A variable's type is a type constraint, not an expression: a use that
// reads a variable of a collection type is compared by the variable's text
// like any other, and only a type that is no constant constraint stays
// undecided.
func TestPolicyDocsVariableTypeConstraint(t *testing.T) {
	const xAddr = "data.aws_iam_policy_document.x"
	tree := func(typ, read string) map[string]string {
		return map[string]string{"main.tf": fmt.Sprintf(`variable "v" {
  type = %s
}

data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.x.json
  name   = %s
}
`, typ, read)}
	}
	uses := func(t *testing.T, head, base map[string]string) string {
		t.Helper()
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		p := policyPair(t, r, ".", xAddr)
		if p.Uses == nil {
			t.Fatalf("pair: %+v", p)
		}
		return *p.Uses
	}
	const read = `var.v == null ? "a" : "b"`
	for _, tc := range []struct {
		name, typ, read, want string
	}{
		{"an unread collection variable", "list(string)", `"a"`, "equal"},
		{"a string variable a use reads", "string", read, "equal"},
		{"a list variable a use reads", "list(string)", read, "equal"},
		{"a map of objects a use reads", `map(object({a = optional(string, "x"), b = optional(list(number))}))`, read, "equal"},
		{"a tuple and a set a use reads", "tuple([set(string), any])", read, "equal"},
		{"an optional default that calls a function", `object({a = optional(string, upper("x"))})`, read, "undecidable"},
		{"an optional default that reads a local", `object({a = optional(string, local.d)})`, read, "undecidable"},
		{"a type that is no constraint", "frob(string)", read, "undecidable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := uses(t, tree(tc.typ, tc.read), tree(tc.typ, tc.read)); got != tc.want {
				t.Errorf("uses = %s, want %s", got, tc.want)
			}
		})
	}
	t.Run("a changed type of a variable a use reads differs", func(t *testing.T) {
		if got := uses(t, tree("list(string)", read), tree("set(string)", read)); got != "differs" {
			t.Errorf("uses = %s, want differs", got)
		}
	})
	t.Run("a type argument of another block is an expression", func(t *testing.T) {
		m := tree("string", "aws_ssm_parameter.s.name")
		m["s.tf"] = "resource \"aws_ssm_parameter\" \"s\" {\n  name = \"s\"\n  type = list(string)\n}\n"
		if got := uses(t, m, m); got != "undecidable" {
			t.Errorf("uses = %s, want undecidable", got)
		}
	})
	t.Run("another argument of the variable stays in the grammar", func(t *testing.T) {
		typ := "list(string)\n  default = [file(\"x\")]"
		if got := uses(t, tree(typ, read), tree(typ, read)); got != "undecidable" {
			t.Errorf("uses = %s, want undecidable", got)
		}
	})
}
