package facts

import (
	"maps"
	"slices"
	"testing"
)

// BenchmarkParseDir parses every directory of the head tree once per
// iteration. The tree is read before the timer starts.
func BenchmarkParseDir(b *testing.B) {
	for _, tr := range benchTrees(b) {
		b.Run(tr.name, func(b *testing.B) {
			t, err := ReadTree(tr.head, DefaultLimits)
			if err != nil {
				b.Fatal(err)
			}
			dirs := slices.Sorted(maps.Keys(t.Dirs))
			b.ReportAllocs()
			for b.Loop() {
				for _, d := range dirs {
					if _, err := ParseDir(t, d, DefaultLimits); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
