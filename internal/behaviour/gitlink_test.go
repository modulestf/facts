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
	cases := map[string]struct{ path string }{
		"outside any documentation path": {"vendor/sub"},
		"under a documentation path":     {"docs/sub"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dirs := make([]string, 2)
			for i := range dirs {
				dirs[i] = writeTree(t, files)
				if err := os.MkdirAll(filepath.Join(dirs[i], filepath.FromSlash(c.path)), 0o755); err != nil {
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
