package benchtree

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/schema"
)

// readAll returns every file Write wrote under dir, by slash path.
func readAll(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fsys := root.FS()
	files := map[string][]byte{}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files[p], err = fs.ReadFile(fsys, p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestWriteShape checks the head tree has the size the benchmarks claim,
// that a second write gives the same bytes, and that the base tree differs
// in the files of Changed only.
func TestWriteShape(t *testing.T) {
	head, again, base := t.TempDir(), t.TempDir(), t.TempDir()
	for _, w := range []struct {
		dir  string
		base bool
	}{{head, false}, {again, false}, {base, true}} {
		if err := Write(w.dir, w.base); err != nil {
			t.Fatal(err)
		}
	}
	h, a, b := readAll(t, head), readAll(t, again), readAll(t, base)
	var n, size int
	for name, data := range h {
		if strings.HasSuffix(name, ".tf") {
			n++
			size += len(data)
		}
	}
	if n < 85 || n > 95 || size < 400_000 || size > 600_000 {
		t.Errorf("head tree has %d .tf files and %d bytes, want about 90 and 500 KB", n, size)
	}
	if len(a) != len(h) {
		t.Fatalf("second write has %d files, first %d", len(a), len(h))
	}
	var changed []string
	for name, data := range h {
		if !bytes.Equal(a[name], data) {
			t.Errorf("%s: second write differs", name)
		}
		if bd, ok := b[name]; !ok {
			t.Errorf("%s: missing from the base tree", name)
		} else if !bytes.Equal(bd, data) {
			changed = append(changed, name)
		}
	}
	slices.Sort(changed)
	if len(b) != len(h) || !slices.Equal(changed, Changed) {
		t.Errorf("base differs in %v (%d files), want %v (%d files)", changed, len(b), Changed, len(h))
	}
}

// TestHandoffLoads checks a handoff of the same shape with few unrelated
// types passes the schema loader. The full one has the same shape, more
// types; decoding it under the race detector takes about a minute.
func TestHandoffLoads(t *testing.T) {
	doc, err := json.Marshal(handoff(20, 10))
	if err != nil {
		t.Fatal(err)
	}
	p := t.TempDir() + "/schema-handoff.json"
	if err := os.WriteFile(p, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Load(p, nil); err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
}

// TestWriteHandoffSize checks the full handoff is tens of MB of JSON and
// within its caps. encoding/json writes map keys sorted, so its bytes are
// the same every run. Its binding to the trees is checked where facts runs
// it, in the setup of BenchmarkRun.
func TestWriteHandoffSize(t *testing.T) {
	p := t.TempDir() + "/schema-handoff.json.gz"
	if err := WriteHandoff(p); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, io.LimitReader(zr, 128<<20))
	if err != nil {
		t.Fatal(err)
	}
	if n < 10<<20 || n > 64<<20 {
		t.Errorf("handoff is %d bytes of JSON, want tens of MB", n)
	}
}
