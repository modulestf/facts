package facts

import (
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/benchtree"
)

// benchTree is one head and base pair the benchmarks read.
type benchTree struct {
	name       string
	head, base string
}

// benchTrees returns a small oracle case and a generated tree of the size
// of a large upstream module (see package benchtree).
func benchTrees(b *testing.B) []benchTree {
	b.Helper()
	small := "../../testdata/oracle/examples-positive"
	dir := b.TempDir()
	head, base := filepath.Join(dir, "head"), filepath.Join(dir, "base")
	if err := benchtree.Write(head, false); err != nil {
		b.Fatal(err)
	}
	if err := benchtree.Write(base, true); err != nil {
		b.Fatal(err)
	}
	return []benchTree{
		{"small", filepath.Join(small, "head"), filepath.Join(small, "base")},
		{"large", head, base},
	}
}

func BenchmarkReadTree(b *testing.B) {
	for _, tr := range benchTrees(b) {
		b.Run(tr.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ReadTree(tr.head, DefaultLimits); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
