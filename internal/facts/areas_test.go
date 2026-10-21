package facts

import (
	"fmt"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"main.tf":                      ". root",
		"docs/usage.md":                ". root",
		"README.md":                    ". root",
		"modules/child/main.tf":        "modules/child submodule",
		"modules/child/sub/x.tf":       "modules/child submodule",
		"examples/complete/main.tf":    "examples/complete example",
		"examples/README.md":           "none",
		"modules/x.tf":                 "none",
		"wrappers/main.tf":             "none",
		"tests/basic.tftest.hcl":       "none",
		"basic.tftest.hcl":             "none",
		".github/workflows/ci.yml":     ". root",
		"CHANGELOG.md":                 ". root",
		".pre-commit-config.yaml":      ". root",
		".editorconfig":                ". root",
		"examples/complete/.gitignore": "examples/complete example",
	}
	for p, want := range cases {
		a, k, ok := classify(p)
		got := "none"
		if ok {
			got = a + " " + k
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", p, got, want)
		}
	}
}

// areaList lists areas as path:kind:untouched:head exists:base exists:"issue".
func areaList(as []*Area, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	var out []string
	for _, a := range as {
		out = append(out, fmt.Sprintf("%s:%s:%v:%v:%v:%q", a.Path, a.Kind, a.Untouched, a.HeadExists, a.BaseExists, a.Issue))
	}
	return strings.Join(out, " ")
}

func TestFindAreasNoBase(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "", "modules/a/main.tf": "", "examples/x/main.tf": "",
		"examples/README.md": "", "examples/y": "->x", "wrappers/a/main.tf": "",
	}))
	// Without a base tree every area is in scope, untouched set or not.
	got := areaList(FindAreas(head, nil, DefaultLimits, true))
	want := `.:root:false:true:false:"" examples/x:example:false:true:false:"" examples/y:example:false:false:false:"examples/y is a symbolic link, not a directory" modules/a:submodule:false:true:false:""`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFindAreasWithBase(t *testing.T) {
	base := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "a", "modules/a/main.tf": "a", "modules/gone/main.tf": "a", "examples/x/main.tf": "a",
	}))
	head := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "a", "modules/a/main.tf": "b", "modules/new/main.tf": "a", "examples/x/main.tf": "a",
		"CHANGELOG.md": "x",
	}))
	// Only a cross-area pass reads untouched areas; without one they are
	// neither listed nor parsed.
	for untouched, want := range map[bool]string{
		false: `.:root:false:true:true:"" modules/a:submodule:false:true:true:"" modules/gone:submodule:false:false:true:"" modules/new:submodule:false:true:false:""`,
		true:  `.:root:false:true:true:"" examples/x:example:true:true:true:"" modules/a:submodule:false:true:true:"" modules/gone:submodule:false:false:true:"" modules/new:submodule:false:true:false:""`,
	} {
		if got := areaList(FindAreas(head, base, DefaultLimits, untouched)); got != want {
			t.Errorf("untouched %v:\ngot  %s\nwant %s", untouched, got, want)
		}
	}
}

// The root is in scope only when the change touches it. A change under
// .github/ or to a root profile file touches the root; a change to an example
// or a submodule alone does not.
func TestFindAreasRootInScopeWhenTouched(t *testing.T) {
	for name, tc := range map[string]struct {
		base, head map[string]string
		want       string
	}{
		"profile file only": {
			base: map[string]string{"main.tf": "a", ".editorconfig": "a"},
			head: map[string]string{"main.tf": "a", ".editorconfig": "b"},
			want: `.:root:false:true:true:""`,
		},
		"workflow only": {
			base: map[string]string{"main.tf": "a"},
			head: map[string]string{"main.tf": "a", ".github/workflows/ci.yml": "b"},
			want: `.:root:false:true:true:""`,
		},
		"example only": {
			base: map[string]string{"main.tf": "a", "examples/x/main.tf": "a"},
			head: map[string]string{"main.tf": "a", "examples/x/main.tf": "b"},
			want: `.:root:true:true:true:"" examples/x:example:false:true:true:""`,
		},
		"submodule only": {
			base: map[string]string{"main.tf": "a", "modules/a/main.tf": "a"},
			head: map[string]string{"main.tf": "a", "modules/a/main.tf": "b"},
			want: `.:root:true:true:true:"" modules/a:submodule:false:true:true:""`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := areaList(FindAreas(mustTree(t, writeTree(t, tc.head)), mustTree(t, writeTree(t, tc.base)), DefaultLimits, true))
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// A change to a submodule alone gives no manifest entry in the root.
func TestSubmoduleOnlyChangeHasNoRootEntries(t *testing.T) {
	base := writeTree(t, map[string]string{"main.tf": "", "modules/a/variables.tf": "variable \"x\" {\n  type = string\n  description = \"X.\"\n}\n"})
	head := writeTree(t, map[string]string{"main.tf": "", "modules/a/variables.tf": "variable \"x\" {\n  type = string\n  description = \"X.\"\n}\nvariable \"y\" {}\n"})
	r, err := Run(Options{Head: head, Base: base, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Manifest) != len(Rules) {
		t.Errorf("want one entry per rule, got %+v", r.Manifest)
	}
	for _, e := range r.Manifest {
		if e.Area != "modules/a" {
			t.Errorf("entry outside the changed submodule: %+v", e)
		}
	}
	if len(r.Candidates) != 1 || r.Candidates[0].Subject != "modules/a:var.y" {
		t.Errorf("candidates %+v", r.Candidates)
	}
}
