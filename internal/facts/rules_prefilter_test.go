package facts

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
)

// The feature walk parses an expression again only when its text holds a
// parenthesis, which every function call has. One that does not parse again
// is still an unknown feature then.
func TestFeatureWalkSkipsTextWithoutCall(t *testing.T) {
	for text, want := range map[string]int{"(": 1, "foo(": 1, "x y": 0, "var.a": 0} {
		m := &Module{Locals: []Local{{Name: "x", Value: Expr{Text: text, Range: hcl.Range{Start: hcl.Pos{Line: 1, Column: 1}}}}}}
		if got := len(featureUses(m)); got != want {
			t.Errorf("%q: %d uses, want %d", text, got, want)
		}
	}
}

// A file is lexed for region literals when its bytes show a region or a
// backslash, which can write one.
func TestMayHoldRegion(t *testing.T) {
	for src, want := range map[string]bool{
		"region = \"eu-west-1\"\n":       true,
		"x = \"us-gov-east-1\"\n":        true,
		"region = \"eu-w\\u0065st-1\"\n": true,
		"region = var.region\n":          false,
		"x = \"eu-west\"\n":              false,
	} {
		if got := mayHoldRegion([]byte(src)); got != want {
			t.Errorf("%q: %v", src, got)
		}
	}
}
