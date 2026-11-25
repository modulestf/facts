package behaviour

import (
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/benchtree"
	"github.com/modulestf/facts/internal/facts"
)

// BenchmarkRun classifies the pull request 556 change, with no schema and
// with one, and the change of the generated large tree (see package
// benchtree).
func BenchmarkRun(b *testing.B) {
	dir := b.TempDir()
	head, base := filepath.Join(dir, "head"), filepath.Join(dir, "base")
	if err := benchtree.Write(head, false); err != nil {
		b.Fatal(err)
	}
	if err := benchtree.Write(base, true); err != nil {
		b.Fatal(err)
	}
	small := Options{Head: filepath.Join(pr556, "head"), Base: filepath.Join(pr556, "base")}
	smallSchema := small
	smallSchema.Schema = filepath.Join(pr556, "schema-optional-computed.json")
	for _, c := range []struct {
		name string
		o    Options
	}{
		{"small", small},
		{"small-schema", smallSchema},
		{"large", Options{Head: head, Base: base}},
	} {
		b.Run(c.name, func(b *testing.B) {
			o := c.o
			o.Limits = facts.DefaultLimits
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Run(o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
