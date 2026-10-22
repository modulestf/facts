package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEngineSections(t *testing.T) {
	text := testEngine(t)
	if len(text) != 6 {
		t.Errorf("%d sections, want 6", len(text))
	}
	got := text["compat.default-changed"]
	want := "## `compat.default-changed`\n\nEngine: `candidate`\n\n### Rule text: `compat.default-changed`\n\n**Fails when:** a default changes.\n"
	if got != want {
		t.Errorf("section %q, want %q", got, want)
	}
}

func writeEngine(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadEngineRefuses(t *testing.T) {
	dup := writeEngine(t, map[string]string{"a.md": "## `x.y`\n", "b.md": "## `x.y`\n"})
	big := writeEngine(t, map[string]string{"a.md": strings.Repeat("x", maxEngineBytes+1)})
	link := writeEngine(t, map[string]string{"target.txt": "## `x.y`\n"})
	if err := os.Symlink(filepath.Join(link, "target.txt"), filepath.Join(link, "a.md")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	escape := writeEngine(t, nil)
	if err := os.WriteFile(filepath.Join(outside, "a.md"), []byte("## `x.y`\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "a.md"), filepath.Join(escape, "a.md")); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"second section": dup, "over the cap": big, "link": link, "link out": escape,
		"missing": filepath.Join(t.TempDir(), "none")} {
		if _, err := LoadEngine(dir); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
	// An empty directory has no sections; other files are skipped.
	if text, err := LoadEngine(t.TempDir()); err != nil || len(text) != 0 {
		t.Errorf("empty directory: %v, %v", text, err)
	}
	ok := writeEngine(t, map[string]string{"notes.txt": "## `x.y`\n", "a.md": "# T\n\n## Not a rule\n\n## `x.y`\nbody\n"})
	text, err := LoadEngine(ok)
	if err != nil || len(text) != 1 || text["x.y"] != "## `x.y`\nbody\n" {
		t.Errorf("text %v, err %v", text, err)
	}
}
