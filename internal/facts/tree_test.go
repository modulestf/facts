//go:build unix

package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadTreeKinds(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.tf":         "# ok\n",
		"link.tf":         "->main.tf",
		"outside.tf":      "->../../etc/passwd",
		"examples/a/x.tf": "",
		"examples/b":      "->a",
		".git/config":     "secret",
		".terraform/x":    "state",
		"big.txt":         strings.Repeat("x", 100),
	})
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.tf"), 0o644); err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits
	lim.MaxFileBytes = 50
	tr, err := ReadTree(root, lim)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"main.tf": KindFile, "link.tf": KindSymlink, "outside.tf": KindSymlink,
		"examples/a/x.tf": KindFile, "examples/b": KindSymlink,
		"pipe.tf": KindSpecial, "big.txt": KindOversize,
	}
	if len(tr.Entries) != len(want) {
		t.Errorf("entries %v, want %d", tr.Paths, len(want))
	}
	for p, k := range want {
		if e := tr.Entries[p]; e == nil || e.Kind != k {
			t.Errorf("%s: got %+v, want kind %s", p, e, k)
		}
	}
	if tr.Entries["link.tf"].Data != nil {
		t.Error("a link was read")
	}
	if !tr.Dirs["examples/a"] || tr.Dirs["examples/b"] {
		t.Errorf("dirs %v", tr.Dirs)
	}
}

func TestReadTreeLimits(t *testing.T) {
	root := writeTree(t, map[string]string{"a.tf": "aaaa", "b.tf": "bbbb", "c.tf": "cccc"})
	lim := DefaultLimits
	lim.MaxFiles = 2
	if _, err := ReadTree(root, lim); err == nil {
		t.Error("file count limit not enforced")
	}
	lim = DefaultLimits
	lim.MaxTotalBytes = 10
	if _, err := ReadTree(root, lim); err == nil {
		t.Error("total bytes limit not enforced")
	}
}

func TestReadTreeUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every file")
	}
	root := writeTree(t, map[string]string{"main.tf": "x"})
	if err := os.Chmod(filepath.Join(root, "main.tf"), 0); err != nil {
		t.Fatal(err)
	}
	if k := mustTree(t, root).Entries["main.tf"].Kind; k != KindUnreadable {
		t.Errorf("kind %s", k)
	}
}

func TestTreeHashStable(t *testing.T) {
	a := mustTree(t, writeTree(t, map[string]string{"main.tf": "x", "m/v.tf": "y"}))
	b := mustTree(t, writeTree(t, map[string]string{"m/v.tf": "y", "main.tf": "x"}))
	c := mustTree(t, writeTree(t, map[string]string{"main.tf": "x", "m/v.tf": "z"}))
	if a.TreeSHA256() != b.TreeSHA256() || a.TreeSHA256() == c.TreeSHA256() {
		t.Error("tree hash depends on order or ignores content")
	}
}

// recordReadDir installs readDirHook for one test and returns the entries
// each directory's ReadDir batches returned.
func recordReadDir(t *testing.T) map[string]int {
	t.Helper()
	seen := map[string]int{}
	readDirHook = func(dir string, n int) { seen[dir] += n }
	t.Cleanup(func() { readDirHook = nil })
	return seen
}

func TestReadTreeStopsListingAtMaxFiles(t *testing.T) {
	root := t.TempDir()
	for i := range 366 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d.tf", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seen := recordReadDir(t)
	lim := DefaultLimits
	lim.MaxFiles = 10
	_, err := ReadTree(root, lim)
	if err == nil || err.Error() != "more than 10 entries" {
		t.Fatalf("err %v", err)
	}
	if seen["."] > lim.MaxFiles+readDirBatch {
		t.Errorf("listed %d entries, want at most %d", seen["."], lim.MaxFiles+readDirBatch)
	}
}

func TestReadTreeSkipDirCountsOnceAndIsNotRead(t *testing.T) {
	files := map[string]string{"a.tf": "", "b.tf": ""}
	for i := range 300 {
		files[fmt.Sprintf(".git/objects/o%03d", i)] = ""
	}
	root := writeTree(t, files)
	seen := recordReadDir(t)
	lim := DefaultLimits
	lim.MaxFiles = 3 // a.tf, b.tf and .git
	tr, err := ReadTree(root, lim)
	if err != nil {
		t.Fatal(err)
	}
	for dir := range seen {
		if dir == ".git" || strings.HasPrefix(dir, ".git/") {
			t.Errorf("read %s", dir)
		}
	}
	if len(tr.Paths) != 2 || tr.Dirs[".git"] {
		t.Errorf("paths %v dirs %v", tr.Paths, tr.Dirs)
	}
	lim.MaxFiles = 2
	if _, err := ReadTree(root, lim); err == nil {
		t.Error(".git not counted")
	}
}

func TestReadTreeUnreadableSubdirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	root := writeTree(t, map[string]string{"a/x.tf": "", "b/y.tf": "", "c.tf": ""})
	locked := filepath.Join(root, "b")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	tr := mustTree(t, root)
	if e := tr.Entries["b"]; e == nil || e.Kind != KindUnreadable {
		t.Errorf("b: %+v", e)
	}
	for _, p := range []string{"a/x.tf", "c.tf"} {
		if e := tr.Entries[p]; e == nil || e.Kind != KindFile {
			t.Errorf("%s: %+v", p, e)
		}
	}
	if _, ok := tr.Entries["b/y.tf"]; ok {
		t.Error("read inside an unreadable directory")
	}
}

func TestOpenDirRefusesNonDirectories(t *testing.T) {
	root := writeTree(t, map[string]string{"real/x.tf": "", "link": "->real", "file.tf": ""})
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, p := range []string{"pipe", "link", "file.tf"} {
		done := make(chan error, 1)
		go func() {
			f, err := openDir(r, p)
			if err == nil {
				_ = f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: opened as a directory", p)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: open blocked", p)
		}
	}
	f, err := openDir(r, "real")
	if err != nil {
		t.Fatalf("real directory: %v", err)
	}
	_ = f.Close()
}
