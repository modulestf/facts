package fixture

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// FuzzFile feeds one arbitrary .tf file to the fixture grammar at each
// level. Every outcome is a violation list; none may panic. The list is
// sorted, holds at most MaxViolations entries and the marker of the rest,
// names only known rules, and never echoes text that is not a plain name.
func FuzzFile(f *testing.F) {
	for _, d := range []string{"l1", "l2", "l3"} {
		matches, err := filepath.Glob(filepath.Join("../../testdata/fixture-check", d, "*.tf"))
		if err != nil {
			f.Fatal(err)
		}
		for _, p := range matches {
			data, err := os.ReadFile(p)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
	known := map[string]bool{RuleLimitViolations: true}
	for _, r := range Rules {
		known[r] = true
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, name := range Levels() {
			c := newChecker(levels[name], "aws_s3_bucket", nil)
			c.file("main.tf", src)
			c.finish()
			r := c.result("")
			if len(r.Violations) > MaxViolations+1 {
				t.Fatalf("%s: %d violations", name, len(r.Violations))
			}
			if !slices.IsSortedFunc(r.Violations, func(a, b Violation) int {
				return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line),
					strings.Compare(a.Rule, b.Rule), strings.Compare(a.Detail, b.Detail))
			}) {
				t.Fatalf("%s: violations not sorted", name)
			}
			if r.OK != (len(r.Violations) == 0) {
				t.Fatalf("%s: ok %v with %d violations", name, r.OK, len(r.Violations))
			}
			for _, v := range r.Violations {
				if !known[v.Rule] {
					t.Fatalf("%s: unknown rule %q", name, v.Rule)
				}
				if v.Detail != "" && !plainName.MatchString(v.Detail) {
					t.Fatalf("%s: detail %q echoed", name, v.Detail)
				}
			}
		}
	})
}
