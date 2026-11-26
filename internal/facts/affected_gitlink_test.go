package facts

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// gitlinkBase is a module with a documentation tree that no read site reaches.
var gitlinkBase = map[string]string{
	"main.tf":                   "resource \"null_resource\" \"this\" {}\n",
	"docs/guide.md":             "# guide\n",
	"examples/complete/main.tf": "module \"this\" {\n  source = \"../../\"\n}\n",
}

// writeGitlinkTree writes files and makes each of dirs an empty directory, or
// one holding only a .git file when withGitFile is set: how a checkout holds
// a submodule's commit pointer.
func writeGitlinkTree(t *testing.T, files map[string]string, dirs []string, withGitFile bool) string {
	t.Helper()
	root := writeTree(t, files)
	for _, d := range dirs {
		full := filepath.Join(root, filepath.FromSlash(d))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if withGitFile {
			if err := os.WriteFile(filepath.Join(full, ".git"), []byte("gitdir: ../.git/modules/x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// notInert fails unless the report names p as changed and is not inert, and,
// with fallback set, gives a fallback reason.
func notInert(t *testing.T, r *AffectedReport, p string, fallback bool) {
	t.Helper()
	if r.Inert {
		t.Errorf("inert = true, want false")
	}
	if fallback && r.Fallback == nil {
		t.Errorf("fallback = nil, want a reason")
	}
	if !slices.Contains(r.Changed, p) {
		t.Errorf("changed = %q, want it to hold %q", r.Changed, p)
	}
	checkAffectedSchema(t, r)
}

// TestAffectedNonRegularChange gives each change of an entry that is not a
// regular file with --base and with --diff. With --base the change is never
// inert and is listed; with --diff the run refuses or is not inert, never
// inert.
func TestAffectedNonRegularChange(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		base, head         map[string]string
		baseDirs, headDirs []string
		path               string
		link               bool   // a link: not inert and listed, with no fallback
		files              string // the GitHub files list entry of the change
	}{
		"gitlink bump": {
			base: gitlinkBase, head: gitlinkBase, baseDirs: []string{"vendor/sub"}, headDirs: []string{"vendor/sub"},
			path:  "vendor/sub",
			files: `[{"filename":"vendor/sub","status":"modified","additions":1,"deletions":1,"changes":2}]`,
		},
		"gitlink bump under docs": {
			base: gitlinkBase, head: gitlinkBase, baseDirs: []string{"docs/sub"}, headDirs: []string{"docs/sub"},
			path:  "docs/sub",
			files: `[{"filename":"docs/sub","status":"modified","additions":1,"deletions":1,"changes":2}]`,
		},
		"file to gitlink": {
			base: with(gitlinkBase, map[string]string{"docs/sub": "text\n"}), head: gitlinkBase, headDirs: []string{"docs/sub"},
			path:  "docs/sub",
			files: `[{"filename":"docs/sub","status":"modified","additions":0,"deletions":1,"changes":1}]`,
		},
		"symlink retarget": {
			base: with(gitlinkBase, map[string]string{"docs/cur.md": "->guide.md"}),
			head: with(gitlinkBase, map[string]string{"docs/cur.md": "->other.md"}),
			path: "docs/cur.md", link: true,
			files: `[{"filename":"docs/cur.md","status":"modified","additions":1,"deletions":1,"changes":2,` +
				`"patch":"@@ -1 +1 @@\n-guide.md\n+other.md\n\\ No newline at end of file"}]`,
		},
	}
	for name, c := range cases {
		for _, gitFile := range []bool{false, true} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				head := writeGitlinkTree(t, c.head, c.headDirs, gitFile)
				base := writeGitlinkTree(t, c.base, c.baseDirs, gitFile)
				r, err := Affected(AffectedOptions{Head: head, Base: base, Limits: DefaultLimits})
				if err != nil {
					t.Fatal(err)
				}
				notInert(t, r, c.path, !c.link)

				list := filepath.Join(t.TempDir(), "files.json")
				if err := os.WriteFile(list, []byte(c.files), 0o600); err != nil {
					t.Fatal(err)
				}
				r, err = Affected(AffectedOptions{Head: head, Diff: list, Limits: DefaultLimits})
				if err == nil && r.Inert {
					t.Errorf("--diff: inert = true, want false or an error")
				}
			})
		}
	}
}

// TestAffectedGitlinkModuleRoot is the same change under a module root: a
// gitlink in the module, in the repository root's examples, and outside both.
func TestAffectedGitlinkModuleRoot(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"mod/main.tf":                   "resource \"null_resource\" \"this\" {}\n",
		"mod/docs/guide.md":             "# guide\n",
		"examples/complete/main.tf":     "module \"this\" {\n  source = \"../../mod\"\n}\n",
		"other/main.tf":                 "resource \"null_resource\" \"other\" {}\n",
		"mod/examples/basic/main.tf":    "module \"this\" {\n  source = \"../..\"\n}\n",
		"mod/examples/basic/README.md":  "# basic\n",
		"examples/complete/README.md":   "# complete\n",
		"other/docs/README.md":          "# other\n",
		"mod/modules/sub/main.tf":       "resource \"null_resource\" \"sub\" {}\n",
		"mod/modules/sub/docs/guide.md": "# sub\n",
	}
	cases := map[string]struct {
		dir     string
		changed bool // the change is in scope of the module root
	}{
		"in the module":                {"mod/docs/sub", true},
		"in a module root example":     {"mod/examples/basic/vendor", true},
		"in a repository root example": {"examples/complete/vendor", true},
		"outside the module":           {"other/docs/sub", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			head := writeGitlinkTree(t, files, []string{c.dir}, false)
			base := writeGitlinkTree(t, files, []string{c.dir}, false)
			r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			checkAffectedSchema(t, r)
			if c.changed {
				notInert(t, r, c.dir, true)
			} else if !r.Inert || r.Fallback != nil {
				t.Errorf("a gitlink outside the module root: inert %v fallback %v, want inert", r.Inert, r.Fallback)
			}
		})
	}
}
