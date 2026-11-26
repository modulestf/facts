package schemadiff

import (
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/benchtree"
	"github.com/modulestf/facts/internal/facts"
)

// BenchmarkRun compares two generated aws schema files of about the size of
// the real provider, at its declared minimum and its latest version, for
// the types of the large generated tree.
func BenchmarkRun(b *testing.B) {
	dir := b.TempDir()
	o := Options{
		From: filepath.Join(dir, "from.json"), To: filepath.Join(dir, "to.json"),
		TypesFrom: filepath.Join(dir, "tree"), Limits: facts.DefaultLimits,
	}
	if err := benchtree.WriteSchema(o.From, true); err != nil {
		b.Fatal(err)
	}
	if err := benchtree.WriteSchema(o.To, false); err != nil {
		b.Fatal(err)
	}
	if err := benchtree.Write(o.TypesFrom, false); err != nil {
		b.Fatal(err)
	}
	r, err := Run(o)
	if err != nil {
		b.Fatal(err)
	}
	if len(r.Changes) == 0 {
		b.Fatal("no change: the run compares nothing the two versions differ in")
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(o); err != nil {
			b.Fatal(err)
		}
	}
}
