package facts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/stats"
)

// TestReadTreeCountsReadBytes checks the stats count every byte read,
// a binary file's too: it is read, found binary and dropped.
func TestReadTreeCountsReadBytes(t *testing.T) {
	dir := t.TempDir()
	tf := []byte("variable \"a\" {}\n")
	bin := []byte{0x50, 0x4b, 0, 0, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), tf, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.zip"), bin, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := stats.New()
	lim := DefaultLimits
	lim.Stats = rec
	if _, err := ReadTree(dir, lim); err != nil {
		t.Fatal(err)
	}
	r := rec.Record("lint", "v", 0)
	if r.FilesRead != 2 || r.BytesRead != int64(len(tf)+len(bin)) {
		t.Errorf("read %d files %d bytes, want 2 and %d", r.FilesRead, r.BytesRead, len(tf)+len(bin))
	}
}

// TestReadTreeCountsHashedBytes checks an oversize file, streamed to its
// hash and not kept, counts as one file read with all its bytes.
func TestReadTreeCountsHashedBytes(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 64)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	rec := stats.New()
	lim := DefaultLimits
	lim.MaxFileBytes, lim.Stats = 16, rec
	tr, err := ReadTree(dir, lim)
	if err != nil {
		t.Fatal(err)
	}
	if e := tr.Entries["big.txt"]; e == nil || e.Kind != KindOversize || e.oversizeSHA256 == "" {
		t.Fatalf("entry %+v, want a hashed oversize file", e)
	}
	r := rec.Record("lint", "v", 0)
	if r.FilesRead != 1 || r.BytesRead != int64(len(big)) {
		t.Errorf("read %d files %d bytes, want 1 and %d", r.FilesRead, r.BytesRead, len(big))
	}
}
