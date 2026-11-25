package facts

import (
	"path/filepath"
	"testing"
)

// BenchmarkRun runs lint on each tree with no change and with the base
// tree, and on the small schema case with its handoff.
func BenchmarkRun(b *testing.B) {
	schema := "../../testdata/coverage-schema-positive"
	cases := []struct {
		name string
		o    Options
	}{{
		name: "small-schema",
		o: Options{
			Head: filepath.Join(schema, "head"), Base: filepath.Join(schema, "base"),
			SchemaHandoff: filepath.Join(schema, "schema-handoff.json"),
			HeadSHA:       oracleHeadSHA, MergeBaseSHA: oracleBaseSHA,
		},
	}}
	for _, tr := range benchTrees(b) {
		cases = append(cases,
			struct {
				name string
				o    Options
			}{tr.name + "-no-change", Options{Head: tr.head}},
			struct {
				name string
				o    Options
			}{tr.name + "-base", Options{Head: tr.head, Base: tr.base}},
		)
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			o := c.o
			o.Limits = DefaultLimits
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Run(o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
