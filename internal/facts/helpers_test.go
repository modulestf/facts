package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// writeProfile copies the test profile to a new temp dir and appends a
// profile-options block with lines. With examples: none it also empties the
// example entries of required-files, as such a profile must.
func writeProfile(t *testing.T, lines ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profile")
	if err := os.CopyFS(dir, os.DirFS(testProfile)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "PROFILE.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if slices.Contains(lines, "examples: none") {
		doc = strings.Replace(doc, "example: main.tf outputs.tf versions.tf README.md\n", "example:\n", 1)
		doc = strings.Replace(doc, "example-dirs: examples/basic examples/complete\n", "example-dirs:\n", 1)
	}
	if len(lines) > 0 {
		doc += "\n```profile-constants\nname: profile-options\n" + strings.Join(lines, "\n") + "\n```\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "PROFILE.md"), []byte(doc), 0o644); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	return dir
}

// inRepoOptions are the options of a profile for modules only called from
// the same repository, which relax every option.
var inRepoOptions = []string{"format: 1", "consumers: in-repo", "partitions: fixed", "examples: none", "release: none"}
