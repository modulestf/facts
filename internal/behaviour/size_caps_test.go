package behaviour

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestUndecomposableReasons: a directory that cannot be decomposed says why
// in the entry kind's own words, so an oversize file is not called a link.
func TestUndecomposableReasons(t *testing.T) {
	t.Parallel()
	base := "variable \"x\" {\n  default = 1\n}\n"
	head := "variable \"x\" {\n  default = 2\n}\n"
	pad := func(n int) string { return "# " + strings.Repeat("x", n) + "\n" }
	for _, c := range []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{"over the size cap", map[string]string{"big.tf": pad(2 << 20)}, "big.tf is over the file size limit"},
		{"a link", map[string]string{"real.txt": "x", "link.tf": "->real.txt"}, "link.tf is a symbolic link"},
		{"over the token cap", map[string]string{"dense.tf": dense(70000)}, "dense.tf is over a parser limit: "},
		{"between 1 and 2 MiB", map[string]string{"pad.tf": pad(3 << 19)}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			files := func(vars string) map[string]string {
				m := map[string]string{"variables.tf": vars}
				for k, v := range c.extra {
					m[k] = v
				}
				return m
			}
			r := runLinked(t, files(base), files(head))
			if c.want == "" {
				for _, tr := range r.Triggers {
					if tr.Category == CatUnclassified {
						t.Errorf("unclassified trigger: %s", str(tr.Reason))
					}
				}
				return
			}
			if len(r.Triggers) != 1 || r.Triggers[0].Category != CatUnclassified || !strings.Contains(str(r.Triggers[0].Reason), c.want) {
				t.Errorf("triggers %+v, want reason containing %q", r.Triggers, c.want)
			}
		})
	}
}

// dense is a .tf file of n attributes, four tokens each.
func dense(n int) string {
	var b strings.Builder
	b.WriteString("locals {\n")
	for i := range n {
		fmt.Fprintf(&b, "  a%d = 1\n", i)
	}
	return b.String() + "}\n"
}

// runLinked is runPair for file maps whose "->target" values are links.
func runLinked(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	build := func(files map[string]string) string {
		plain := map[string]string{}
		links := map[string]string{}
		for p, body := range files {
			if target, ok := strings.CutPrefix(body, "->"); ok {
				links[p] = target
			} else {
				plain[p] = body
			}
		}
		root := writeTree(t, plain)
		for p, target := range links {
			if err := os.Symlink(target, filepath.Join(root, p)); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	r, err := Run(Options{Head: build(head), Base: build(base), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
