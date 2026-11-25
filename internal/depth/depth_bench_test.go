package depth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/benchtree"
	"github.com/modulestf/facts/internal/facts"
)

// benchOptions writes the classify and affected reports of the change from
// base to head, and the files list when files is empty, and returns the
// options of a depth run on them. The reports are made before the timer
// starts, as the host makes them before it runs depth.
func benchOptions(b *testing.B, head, base, files string, changed []string) Options {
	b.Helper()
	dir := b.TempDir()
	write := func(name string, v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			b.Fatal(err)
		}
		return p
	}
	if files == "" {
		list := make([]map[string]any, 0, len(changed))
		for _, c := range changed {
			list = append(list, map[string]any{"filename": c, "status": "modified", "additions": 1, "deletions": 1})
		}
		files = write("files.json", list)
	}
	cls, err := behaviour.Run(behaviour.Options{Head: head, Base: base, Limits: facts.DefaultLimits})
	if err != nil {
		b.Fatal(err)
	}
	aff, err := facts.Affected(facts.AffectedOptions{Head: head, Base: base, Limits: facts.DefaultLimits})
	if err != nil {
		b.Fatal(err)
	}
	return Options{
		Head: head, Base: base, Files: files,
		Classify: write("classify.json", cls), Affected: write("affected.json", aff),
		Limits: facts.DefaultLimits,
	}
}

// BenchmarkRun decides the depth of the pull request 556 change and of the
// change of the generated large tree (see package benchtree).
func BenchmarkRun(b *testing.B) {
	pr556 := "../behaviour/testdata/pr556"
	dir := b.TempDir()
	head, base := filepath.Join(dir, "head"), filepath.Join(dir, "base")
	if err := benchtree.Write(head, false); err != nil {
		b.Fatal(err)
	}
	if err := benchtree.Write(base, true); err != nil {
		b.Fatal(err)
	}
	for _, c := range []struct {
		name string
		o    Options
	}{
		{"small", benchOptions(b, filepath.Join(pr556, "head"), filepath.Join(pr556, "base"), filepath.Join(pr556, "files.json"), nil)},
		{"large", benchOptions(b, head, base, "", benchtree.Changed)},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Run(c.o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
