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
		"examples/README.md":           "examples unknown",
		"modules/x.tf":                 "modules unknown",
		"wrappers/main.tf":             "none",
		"tests/basic.tftest.hcl":       "none",
		"basic.tftest.hcl":             "none",
		".github/workflows/ci.yml":     "none",
		"CHANGELOG.md":                 "none",
		".pre-commit-config.yaml":      "none",
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

func areaList(as []*Area) string {
	var out []string
	for _, a := range as {
		out = append(out, fmt.Sprintf("%s:%s:%v:%v:%q", a.Path, a.Kind, a.HeadExists, a.BaseExists, a.Issue))
	}
	return strings.Join(out, " ")
}

func TestFindAreasNoBase(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "", "modules/a/main.tf": "", "examples/x/main.tf": "",
		"examples/README.md": "", "examples/y": "->x", "wrappers/a/main.tf": "",
	}))
	got := areaList(FindAreas(head, nil, DefaultLimits))
	want := `.:root:true:false:"" examples/x:example:true:false:"" examples/y:example:false:false:"examples/y is a symbolic link, not a directory" modules/a:submodule:true:false:""`
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
	got := areaList(FindAreas(head, base, DefaultLimits))
	want := `modules/a:submodule:true:true:"" modules/gone:submodule:false:true:"" modules/new:submodule:true:false:""`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
