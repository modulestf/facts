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

// gitlinkRootFiles is a module under mod with a root example that calls it, a
// gitlink at vendor/sub outside both, and files that name it.
func gitlinkRootFiles(set map[string]string) map[string]string {
	return with(map[string]string{
		"mod/main.tf":               "resource \"null_resource\" \"this\" {}\n",
		"mod/docs/guide.md":         "# guide\n",
		"examples/complete/main.tf": "module \"this\" {\n  source = \"../../mod\"\n}\n",
		"vendor/keep.md":            "# keep\n",
	}, set)
}

// TestAffectedGitlinkRead gives a gitlink outside the module root that a read
// site of the module, of a listed root example or a module call in scope
// reaches: the change is in scope, listed, and not inert.
func TestAffectedGitlinkRead(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]string{
		"file in the module": {
			"mod/main.tf": "locals {\n  x = file(\"${path.module}/../vendor/sub/x.txt\")\n}\n",
		},
		"the gitlink itself in the module": {
			"mod/main.tf": "locals {\n  x = file(\"${path.module}/../vendor/sub\")\n}\n",
		},
		"file in a root example": {
			"examples/complete/main.tf": "module \"this\" {\n  source = \"../../mod\"\n}\n" +
				"locals {\n  x = file(\"${path.module}/../../vendor/sub/x.txt\")\n}\n",
		},
		"module call into the gitlink": {
			"mod/main.tf": "module \"v\" {\n  source = \"../vendor/sub\"\n}\n",
		},
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := gitlinkRootFiles(set)
			head := writeGitlinkTree(t, files, []string{"vendor/sub"}, false)
			base := writeGitlinkTree(t, files, []string{"vendor/sub"}, false)
			r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			notInert(t, r, "vendor/sub", true)
		})
	}
}

// TestAffectedGitlinkNotChanged gives a change that leaves a gitlink alone,
// with --diff: no tree holds a changed gitlink, so there is no fallback and
// the gitlink is not in changed.
func TestAffectedGitlinkNotChanged(t *testing.T) {
	t.Parallel()
	head := with(gitlinkBase, map[string]string{"docs/guide.md": "# guide 2\n"})
	list := filepath.Join(t.TempDir(), "files.json")
	files := `[{"filename":"docs/guide.md","status":"modified","additions":1,"deletions":1,` +
		`"patch":"@@ -1 +1 @@\n-# guide\n+# guide 2"}]`
	if err := os.WriteFile(list, []byte(files), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("whole tree", func(t *testing.T) {
		dir := writeGitlinkTree(t, head, []string{"vendor/sub", "docs/sub"}, false)
		r, err := Affected(AffectedOptions{Head: dir, Diff: list, Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		wantAffected(t, r, true, "", "all examples/complete")
		if !slices.Equal(r.Changed, []string{"docs/guide.md"}) {
			t.Errorf("changed = %q, want only the file", r.Changed)
		}
	})
	t.Run("module root", func(t *testing.T) {
		files := gitlinkRootFiles(map[string]string{"mod/docs/guide.md": "# guide 2\n"})
		dir := writeGitlinkTree(t, files, []string{"vendor/sub", "mod/docs/sub"}, false)
		list := filepath.Join(t.TempDir(), "files.json")
		body := `[{"filename":"mod/docs/guide.md","status":"modified","additions":1,"deletions":1,` +
			`"patch":"@@ -1 +1 @@\n-# guide\n+# guide 2"}]`
		if err := os.WriteFile(list, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Affected(AffectedOptions{Head: dir, Diff: list, ModuleRoot: "mod", Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if r.Fallback != nil || !r.Inert || !slices.Equal(r.Changed, []string{"mod/docs/guide.md"}) {
			t.Errorf("inert %v fallback %v changed %q, want inert, no fallback, the file only", r.Inert, r.Fallback, r.Changed)
		}
	})
}

// TestAffectedGitlinkFileToGitlinkModuleRoot replaces a file of the module
// with a gitlink, under a module root.
func TestAffectedGitlinkFileToGitlinkModuleRoot(t *testing.T) {
	t.Parallel()
	files := gitlinkRootFiles(nil)
	head := writeGitlinkTree(t, files, []string{"mod/docs/sub"}, false)
	base := writeGitlinkTree(t, with(files, map[string]string{"mod/docs/sub": "text\n"}), nil, false)
	r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	notInert(t, r, "mod/docs/sub", true)
}

// TestFindAreasGitlink: a changed gitlink touches the area that holds it.
func TestFindAreasGitlink(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf":            "resource \"null_resource\" \"this\" {}\n",
		"modules/x/main.tf":  "resource \"null_resource\" \"x\" {}\n",
		"modules/y/main.tf":  "resource \"null_resource\" \"y\" {}\n",
		"examples/a/main.tf": "module \"m\" {\n  source = \"../..\"\n}\n",
	}
	head := mustTree(t, writeGitlinkTree(t, files, []string{"modules/x/vendor/s"}, false))
	base := mustTree(t, writeGitlinkTree(t, files, []string{"modules/x/vendor/s"}, false))
	areas, err := FindAreas(head, base, DefaultLimits, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range areas {
		if !a.Untouched {
			got = append(got, a.Path)
		}
	}
	if !slices.Equal(got, []string{"modules/x"}) {
		t.Errorf("touched areas = %q, want modules/x", got)
	}
}
