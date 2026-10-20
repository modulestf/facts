//go:build unix

package facts

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
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
