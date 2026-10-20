package facts

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTree creates files under a new temp dir. A value starting with "->"
// makes a symbolic link to the rest of the value.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		var err error
		if len(body) > 2 && body[:2] == "->" {
			err = os.Symlink(body[2:], full)
		} else {
			err = os.WriteFile(full, []byte(body), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustTree(t *testing.T, root string) *Tree {
	t.Helper()
	tr, err := ReadTree(root, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}
