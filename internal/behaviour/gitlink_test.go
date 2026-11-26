package behaviour

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestGitlinkChangeIsNotInert gives a change of a submodule commit pointer,
// which a checkout holds as an empty directory with no content to compare:
// the report is not inert and holds an unclassified trigger for it.
func TestGitlinkChangeIsNotInert(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf":       "resource \"null_resource\" \"this\" {}\n",
		"docs/guide.md": "# guide\n",
	}
	cases := map[string]struct {
		path     string
		baseFile bool // the base holds a file where the head holds the gitlink
		headFile bool // the head holds a file where the base holds the gitlink
	}{
		"outside any documentation path": {path: "vendor/sub"},
		"under a documentation path":     {path: "docs/sub"},
		"file to gitlink":                {path: "docs/sub", baseFile: true},
		"gitlink to file":                {path: "docs/sub", headFile: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dirs := make([]string, 2) // head, base
			for i := range dirs {
				dirs[i] = writeTree(t, files)
				full := filepath.Join(dirs[i], filepath.FromSlash(c.path))
				if i == 1 && c.baseFile || i == 0 && c.headFile {
					if err := os.WriteFile(full, []byte("text\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				} else if err := os.MkdirAll(full, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Run(Options{Head: dirs[0], Base: dirs[1], Limits: facts.DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			if r.Inert {
				t.Error("inert = true, want false")
			}
			if len(r.Triggers) != 1 || r.Triggers[0].File != c.path || r.Triggers[0].Category != CatUnclassified {
				t.Errorf("triggers = %+v, want one unclassified trigger for %s", r.Triggers, c.path)
			}
			if len(r.Entries) != 1 || r.Entries[0].Path != c.path || r.Entries[0].Inert {
				t.Errorf("entries = %+v, want one entry for %s that is not inert", r.Entries, c.path)
			}
		})
	}
}
