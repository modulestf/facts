package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestAffectedGitlinkToFileModuleRoot replaces a gitlink of the module with a
// file, and reaches a gitlink through a module call that only one tree holds.
func TestAffectedGitlinkToFileModuleRoot(t *testing.T) {
	t.Parallel()
	files := gitlinkRootFiles(nil)
	t.Run("in the module", func(t *testing.T) {
		t.Parallel()
		head := writeGitlinkTree(t, with(files, map[string]string{"mod/docs/sub": "text\n"}), nil, false)
		base := writeGitlinkTree(t, files, []string{"mod/docs/sub"}, false)
		r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		notInert(t, r, "mod/docs/sub", true)
	})
	call := with(files, map[string]string{"mod/main.tf": "module \"v\" {\n  source = \"../vendor/sub\"\n}\n"})
	t.Run("module call, gitlink only at head", func(t *testing.T) {
		t.Parallel()
		head := writeGitlinkTree(t, call, []string{"vendor/sub"}, false)
		base := writeGitlinkTree(t, with(call, map[string]string{"vendor/sub": "text\n"}), nil, false)
		r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		notInert(t, r, "vendor/sub", true)
	})
	t.Run("module call, gitlink only at base", func(t *testing.T) {
		t.Parallel()
		head := writeGitlinkTree(t, with(call, map[string]string{"vendor/sub": "text\n"}), nil, false)
		base := writeGitlinkTree(t, call, []string{"vendor/sub"}, false)
		r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		notInert(t, r, "vendor/sub", true)
	})
}

// TestAffectedGitlinkFallbackKeepsRootExample: a fallback for another cause
// still lists a changed file of a root example and the gitlink in scope.
func TestAffectedGitlinkFallbackKeepsRootExample(t *testing.T) {
	t.Parallel()
	files := gitlinkRootFiles(map[string]string{"mod/main.tf": "module \"d\" {\n  source = var.src\n}\n"})
	head := writeGitlinkTree(t, with(files, map[string]string{"examples/complete/README.md": "# x\n"}), []string{"mod/docs/sub"}, false)
	base := writeGitlinkTree(t, files, []string{"mod/docs/sub"}, false)
	r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if r.Fallback == nil || *r.Fallback != FallbackDynamicSource {
		t.Fatalf("fallback = %v, want %s", r.Fallback, FallbackDynamicSource)
	}
	if want := []string{"examples/complete/README.md", "mod/docs/sub"}; !slices.Equal(r.Changed, want) {
		t.Errorf("changed = %q, want %q", r.Changed, want)
	}
}

// TestReadTreeRootIsNoGitlink: a tree holding nothing but a .git file is no
// gitlink; only a directory below the root can be one.
func TestReadTreeRootIsNoGitlink(t *testing.T) {
	t.Parallel()
	dir := writeGitlinkTree(t, nil, []string{"sub"}, true)
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mustTree(t, writeGitlinkTree(t, nil, nil, true)).IsGitlink(".") {
		t.Error("an empty root is a gitlink")
	}
	only := t.TempDir()
	if err := os.WriteFile(filepath.Join(only, ".git"), []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mustTree(t, only).IsGitlink(".") {
		t.Error("a root with only a .git file is a gitlink")
	}
	tr := mustTree(t, dir)
	if tr.IsGitlink(".") || !tr.IsGitlink("sub") {
		t.Errorf("gitlinks: root %v, sub %v, want false, true", tr.IsGitlink("."), tr.IsGitlink("sub"))
	}
}

// TestFindAreasGitlink: a changed gitlink puts every area in scope, since a
// module call below it cannot be read, and is an area of its own.
func TestFindAreasGitlink(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf":            "resource \"null_resource\" \"this\" {}\n",
		"modules/x/main.tf":  "resource \"null_resource\" \"x\" {}\n",
		"modules/y/main.tf":  "resource \"null_resource\" \"y\" {}\n",
		"examples/a/main.tf": "module \"m\" {\n  source = \"../..\"\n}\n",
	}
	touched := func(t *testing.T, dirs []string, withBase bool) []string {
		t.Helper()
		head := mustTree(t, writeGitlinkTree(t, files, dirs, false))
		var base *Tree
		if withBase {
			base = mustTree(t, writeGitlinkTree(t, files, dirs, false))
		} else {
			base = mustTree(t, writeGitlinkTree(t, files, nil, false))
		}
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
		return got
	}
	for name, dirs := range map[string][]string{"in an area": {"modules/x/vendor/s"}, "outside modules": {"vendor/sub"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := touched(t, dirs, true)
			want := []string{".", "examples/a", "modules/x", "modules/y"}
			if dirs[0] == "vendor/sub" {
				want = []string{".", "examples/a", "modules/x", "modules/y", "vendor/sub"}
			}
			if !slices.Equal(got, want) {
				t.Errorf("touched areas = %q, want %q", got, want)
			}
		})
	}
}

// lintGitlink runs every rule on a head and a base that both hold the
// gitlink at dir, or on a diff that bumps it.
func lintGitlink(t *testing.T, files map[string]string, dir string, diff bool) *Report {
	t.Helper()
	o := Options{Head: writeGitlinkTree(t, files, []string{dir}, false), ProfileDir: testProfile, Limits: DefaultLimits}
	if diff {
		f := filepath.Join(t.TempDir(), "change")
		patch := "@@ -1 +1 @@\\n-Subproject commit " + gitSHA + "\\n+Subproject commit " + gitSHA[:39] + "0"
		if err := os.WriteFile(f, []byte(filesList("modified", dir, patch)), 0o600); err != nil {
			t.Fatal(err)
		}
		o.Diff = f
	} else {
		o.Base = writeGitlinkTree(t, files, []string{dir}, false)
	}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestGitlinkCalleeIsNotEvaluated: a rule that reads into a module a call
// names, which is a gitlink, is not evaluated in the calling area, whether the
// gitlink changed or not, and the calling area is in scope.
func TestGitlinkCalleeIsNotEvaluated(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf":           "module \"s\" {\n  source = \"./sub\"\n  foo = 1\n}\n",
		"modules/m/main.tf": "module \"s\" {\n  source = \"../../sub/inner\"\n  foo = 1\n}\n",
	}
	rules := []string{"scope.module-call", "coverage.unknown-argument", "structure.passthrough-default-drift", "structure.passthrough-field-drift"}
	for name, diff := range map[string]bool{"base trees": false, "diff": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := lintGitlink(t, files, "sub", diff)
			for _, area := range []string{".", "modules/m"} {
				if !slices.Contains(r.Scope.AreasChecked, area) {
					t.Errorf("%s not checked: %+v", area, r.Scope)
				}
				for _, rule := range rules {
					if area != "." && strings.HasPrefix(rule, "structure.passthrough") {
						continue // a cross-area pass has its one entry under the root
					}
					if s := statusOf(r.Manifest, rule, area); !strings.HasPrefix(s, StatusNotEvaluated) {
						t.Errorf("%s in %s: %s, want not_evaluated", rule, area, s)
					}
				}
			}
		})
	}
	t.Run("a call into a directory is still read", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{
			"main.tf":           "module \"s\" {\n  source = \"./sub\"\n  foo = 1\n}\n",
			"sub/main.tf":       "variable \"foo\" {\n  type = number\n}\n",
			"modules/m/main.tf": "resource \"null_resource\" \"m\" {}\n",
		}
		r, err := Run(Options{Head: writeTree(t, files), Base: writeTree(t, files), ProfileDir: testProfile, Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if s := statusOf(r.Manifest, "scope.module-call", "."); strings.HasPrefix(s, StatusNotEvaluated) {
			t.Errorf("scope.module-call: %s", s)
		}
	})
}

const gitSHA = "0123456789abcdef0123456789abcdef01234567"

// diffAffected runs Affected on a head and a change given as a diff text.
func diffAffected(t *testing.T, head, diff, root string) *AffectedReport {
	t.Helper()
	f := filepath.Join(t.TempDir(), "change")
	if err := os.WriteFile(f, []byte(diff), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Affected(AffectedOptions{Head: head, Diff: f, ModuleRoot: root, Limits: DefaultLimits})
	if err != nil {
		t.Fatalf("Affected: %v", err)
	}
	checkAffectedSchema(t, r)
	return r
}

func filesList(status, p, patch string) string {
	return `[{"filename":"` + p + `","status":"` + status + `","additions":1,"deletions":1,"patch":"` + patch + `"}]`
}

// TestAffectedDiffGitlink gives each way a diff changes a submodule pointer,
// in a files list and in a unified diff: none is inert, each lists the path.
func TestAffectedDiffGitlink(t *testing.T) {
	t.Parallel()
	read := with(gitlinkBase, map[string]string{"main.tf": "locals {\n  p = file(\"${path.module}/docs/sub/policy.json\")\n}\n"})
	sub := "Subproject commit " + gitSHA
	cases := map[string]struct {
		files map[string]string
		dirs  []string // gitlinks at the head
		diff  string
	}{
		"files list, gitlink removed":         {gitlinkBase, nil, filesList("removed", "docs/sub", "@@ -1 +0,0 @@\\n-"+sub)},
		"files list, gitlink removed, read":   {read, nil, filesList("removed", "docs/sub", "@@ -1 +0,0 @@\\n-"+sub)},
		"files list, gitlink bumped":          {gitlinkBase, []string{"docs/sub"}, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-"+sub+"\\n+"+sub+"1")},
		"files list, gitlink to file":         {with(gitlinkBase, map[string]string{"docs/sub": "text\n"}), nil, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-"+sub+"\\n+text")},
		"files list, file to gitlink":         {gitlinkBase, []string{"docs/sub"}, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-text\\n+"+sub)},
		"files list, gitlink added":           {gitlinkBase, []string{"docs/sub"}, filesList("added", "docs/sub", "@@ -0,0 +1 @@\\n+"+sub)},
		"unified, gitlink removed":            {gitlinkBase, nil, "diff --git a/docs/sub b/docs/sub\ndeleted file mode 160000\nindex " + gitSHA[:7] + "..0000000\n--- a/docs/sub\n+++ /dev/null\n@@ -1 +0,0 @@\n-" + sub + "\n"},
		"unified, gitlink bumped":             {gitlinkBase, []string{"docs/sub"}, "diff --git a/docs/sub b/docs/sub\nindex abc1234..def5678 160000\n--- a/docs/sub\n+++ b/docs/sub\n@@ -1 +1 @@\n-" + sub + "\n+" + sub + "1\n"},
		"unified, gitlink added":              {gitlinkBase, []string{"docs/sub"}, "diff --git a/docs/sub b/docs/sub\nnew file mode 160000\nindex 0000000..def5678\n--- /dev/null\n+++ b/docs/sub\n@@ -0,0 +1 @@\n+" + sub + "\n"},
		"unified, mode change to a gitlink":   {gitlinkBase, []string{"docs/sub"}, "diff --git a/docs/sub b/docs/sub\nold mode 100644\nnew mode 160000\n"},
		"unified, mode change from a gitlink": {with(gitlinkBase, map[string]string{"docs/sub": "x\n"}), nil, "diff --git a/docs/sub b/docs/sub\nold mode 160000\nnew mode 100644\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			head := writeGitlinkTree(t, c.files, c.dirs, false)
			r := diffAffected(t, head, c.diff, "")
			notInert(t, r, "docs/sub", true)
		})
	}
	t.Run("a mode line of an ordinary file is no gitlink", func(t *testing.T) {
		t.Parallel()
		head := writeGitlinkTree(t, gitlinkBase, nil, false)
		r := diffAffected(t, head, "diff --git a/docs/guide.md b/docs/guide.md\nold mode 100755\nnew mode 100644\n", "")
		if r.Fallback != nil || slices.Contains(r.Changed, "docs/sub") {
			t.Errorf("fallback %v changed %q", r.Fallback, r.Changed)
		}
	})
	t.Run("module root", func(t *testing.T) {
		t.Parallel()
		files := gitlinkRootFiles(map[string]string{"mod/main.tf": "locals {\n  p = file(\"${path.module}/docs/sub/policy.json\")\n}\n"})
		head := writeGitlinkTree(t, files, nil, false)
		r := diffAffected(t, head, filesList("removed", "mod/docs/sub", "@@ -1 +0,0 @@\\n-"+sub), "mod")
		notInert(t, r, "mod/docs/sub", true)
	})
}

// TestParseUnifiedGitlinkMarks checks which side of a change each mode line
// and hunk line shows as a gitlink.
func TestParseUnifiedGitlinkMarks(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	cases := map[string]struct {
		diff           string
		oldGit, newGit bool
	}{
		"deleted file mode":           {"diff --git a/s b/s\ndeleted file mode 160000\n--- a/s\n+++ /dev/null\n@@ -1 +0,0 @@\n-" + sub + "\n", true, false},
		"new file mode":               {"diff --git a/s b/s\nnew file mode 160000\n--- /dev/null\n+++ b/s\n@@ -0,0 +1 @@\n+" + sub + "\n", false, true},
		"deleted file mode, no hunk":  {"diff --git a/s b/s\ndeleted file mode 160000\n", true, false},
		"new file mode, no hunk":      {"diff --git a/s b/s\nnew file mode 160000\n", false, true},
		"deleted file mode of a file": {"diff --git a/s b/s\ndeleted file mode 100644\n", false, false},
		"new file mode of a file":     {"diff --git a/s b/s\nnew file mode 100644\n", false, false},
		"old mode of a file":          {"diff --git a/s b/s\nold mode 100644\nnew mode 100755\n", false, false},
		"old mode":                    {"diff --git a/s b/s\nold mode 160000\nnew mode 100644\n", true, false},
		"new mode":                    {"diff --git a/s b/s\nold mode 100644\nnew mode 160000\n", false, true},
		"index line":                  {"diff --git a/s b/s\nindex abc1234..def5678 160000\n--- a/s\n+++ b/s\n@@ -1 +1 @@\n-x\n+y\n", true, true},
		"hunk lines":                  {"diff --git a/s b/s\n--- a/s\n+++ b/s\n@@ -1 +1 @@\n-" + sub + "\n+text\n", true, false},
		"ordinary file":               {"diff --git a/s b/s\nindex abc1234..def5678 100644\n--- a/s\n+++ b/s\n@@ -1 +1 @@\n-Subproject\n+text\n", false, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := parseUnified([]byte(c.diff))
			if err != nil || len(got) != 1 {
				t.Fatalf("parse: %v %v", got, err)
			}
			if got[0].oldGit != c.oldGit || got[0].newGit != c.newGit {
				t.Errorf("old %v new %v, want %v %v", got[0].oldGit, got[0].newGit, c.oldGit, c.newGit)
			}
		})
	}
}

// TestGitlinkAreaIsNotEvaluated: lint reads a gitlink that holds an area as
// an area with an issue, so its rules are not evaluated.
func TestGitlinkAreaIsNotEvaluated(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"main.tf":            "resource \"null_resource\" \"this\" {}\n",
		"modules/ok/main.tf": "resource \"null_resource\" \"ok\" {}\n",
	}
	head := mustTree(t, writeGitlinkTree(t, files, []string{"modules/sub"}, false))
	base := mustTree(t, writeGitlinkTree(t, files, []string{"modules/sub"}, false))
	areas, err := FindAreas(head, base, DefaultLimits, false)
	if err != nil {
		t.Fatal(err)
	}
	var sub *Area
	for _, a := range areas {
		if a.Path == "modules/sub" {
			sub = a
		}
	}
	if sub == nil {
		t.Fatalf("no area modules/sub in %d areas", len(areas))
	}
	if sub.Issue == "" || sub.issueCode == "" {
		t.Errorf("head issue %q code %q, want an issue", sub.Issue, sub.issueCode)
	}
	if sub.Base == nil || len(sub.Base.Unsupported) == 0 {
		t.Errorf("base is not marked unsupported: %+v", sub.Base)
	}
}

// TestAffectedGitlinkCalls: a module call into a gitlink, an example that is
// a gitlink, and a template read below one are walks the tree cannot follow.
func TestAffectedGitlinkCalls(t *testing.T) {
	t.Parallel()
	// With --diff an unchanged gitlink is no change, so only the walk can say.
	modDoc := `[{"filename":"mod/docs/guide.md","status":"modified","additions":1,"deletions":1,"patch":"@@ -1 +1 @@\n-# guide\n+# guide 2"}]`
	t.Run("module root, an example calling a gitlink", func(t *testing.T) {
		t.Parallel()
		files := gitlinkRootFiles(map[string]string{"examples/other/main.tf": "module \"v\" {\n  source = \"../../vendor/sub\"\n}\n", "mod/docs/guide.md": "# guide 2\n"})
		r := diffAffected(t, writeGitlinkTree(t, files, []string{"vendor/sub"}, false), modDoc, "mod")
		if r.Inert || !slices.Contains(rootPairs(r), "all examples/other unresolved") {
			t.Errorf("inert %v examples %q, want not inert with examples/other unresolved", r.Inert, rootPairs(r))
		}
	})
	t.Run("module root, an example that is a gitlink", func(t *testing.T) {
		t.Parallel()
		files := gitlinkRootFiles(map[string]string{"mod/docs/guide.md": "# guide 2\n"})
		r := diffAffected(t, writeGitlinkTree(t, files, []string{"examples/submod"}, false), modDoc, "mod")
		if r.Inert || !slices.Contains(rootPairs(r), "all examples/submod unresolved") {
			t.Errorf("inert %v examples %q, want not inert with examples/submod unresolved", r.Inert, rootPairs(r))
		}
	})
	t.Run("module root, an own example that is a gitlink", func(t *testing.T) {
		t.Parallel()
		files := gitlinkRootFiles(map[string]string{"mod/docs/guide.md": "# guide 2\n"})
		r := diffAffected(t, writeGitlinkTree(t, files, []string{"mod/examples/submod"}, false), modDoc, "mod")
		if r.Inert || r.Fallback == nil || !slices.Contains(rootPairs(r), "all mod/examples/submod") {
			t.Errorf("inert %v fallback %v examples %q", r.Inert, r.Fallback, rootPairs(r))
		}
	})
	readme := `[{"filename":"README.md","status":"modified","additions":1,"deletions":1,"patch":"@@ -1 +1 @@\n-# a\n+# b"}]`
	cases := map[string]map[string]string{
		"a module call into an unchanged gitlink": {"main.tf": "module \"v\" {\n  source = \"./vendor/sub\"\n}\n"},
		"a template read below a gitlink":         {"main.tf": "locals {\n  t = templatefile(\"${path.module}/vendor/sub/t.tpl\", {})\n}\n"},
	}
	for name, set := range cases {
		t.Run("diff, "+name, func(t *testing.T) {
			t.Parallel()
			files := with(gitlinkBase, with(set, map[string]string{"README.md": "# b\n"}))
			head := writeGitlinkTree(t, files, []string{"vendor/sub"}, false)
			r := diffAffected(t, head, readme, "")
			if r.Inert || r.Fallback == nil || *r.Fallback != FallbackUnparsed {
				t.Errorf("inert %v fallback %v, want not inert, unparsed", r.Inert, r.Fallback)
			}
		})
	}
	t.Run("diff, an unchanged gitlink an example is", func(t *testing.T) {
		t.Parallel()
		files := with(gitlinkBase, map[string]string{"README.md": "# b\n"})
		head := writeGitlinkTree(t, files, []string{"examples/submod"}, false)
		r := diffAffected(t, head, readme, "")
		if r.Inert || r.Fallback == nil {
			t.Errorf("inert %v fallback %v, want a fallback", r.Inert, r.Fallback)
		}
	})
}

// TestDiffBaseGitlinkEntries checks what the rebuilt base holds at a path the
// diff shows as a gitlink: no file where the base had a gitlink or nothing,
// an unknown file where the head holds the gitlink alone.
func TestDiffBaseGitlinkEntries(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	cases := map[string]struct {
		files   map[string]string
		dirs    []string
		diff    string
		want    string // the kind at the base, or "" for none
		changed []string
	}{
		"bumped":        {gitlinkBase, []string{"docs/sub"}, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-"+sub+"\\n+"+sub+"1"), "", []string{"docs/sub"}},
		"removed":       {gitlinkBase, nil, filesList("removed", "docs/sub", "@@ -1 +0,0 @@\\n-"+sub), "", []string{"docs/sub"}},
		"added":         {gitlinkBase, []string{"docs/sub"}, filesList("added", "docs/sub", "@@ -0,0 +1 @@\\n+"+sub), "", []string{"docs/sub"}},
		"to a file":     {with(gitlinkBase, map[string]string{"docs/sub": "text\n"}), nil, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-"+sub+"\\n+text"), "", []string{"docs/sub"}},
		"from a file":   {gitlinkBase, []string{"docs/sub"}, filesList("modified", "docs/sub", "@@ -1 +1 @@\\n-text\\n+"+sub), KindNoPatch, []string{"docs/sub"}},
		"not a gitlink": {gitlinkBase, []string{"docs/sub"}, filesList("modified", "docs/guide.md", "@@ -1 +1 @@\\n-# old\\n+# guide"), KindFile, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			headDir := writeGitlinkTree(t, c.files, c.dirs, false)
			head := mustTree(t, headDir)
			f := filepath.Join(t.TempDir(), "change")
			if err := os.WriteFile(f, []byte(c.diff), 0o600); err != nil {
				t.Fatal(err)
			}
			base, _, err := ReadDiffBase(head, f, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			p := "docs/sub"
			if name == "not a gitlink" {
				p = "docs/guide.md"
			}
			got := ""
			if e := base.Entries[p]; e != nil {
				got = e.Kind
			}
			if got != c.want {
				t.Errorf("base entry kind %q, want %q", got, c.want)
			}
			if gl := ChangedGitlinks(head, base); !slices.Equal(gl, c.changed) {
				t.Errorf("changed gitlinks %q, want %q", gl, c.changed)
			}
		})
	}
}

// TestAffectedGitlinkReadModuleRootDiff: an unchanged gitlink a module file
// reads is a fallback with --diff, as the walk cannot see below it.
func TestAffectedGitlinkReadModuleRootDiff(t *testing.T) {
	t.Parallel()
	doc := `[{"filename":"mod/docs/guide.md","status":"modified","additions":1,"deletions":1,"patch":"@@ -1 +1 @@\n-# guide\n+# guide 2"}]`
	read := gitlinkRootFiles(map[string]string{
		"mod/main.tf":       "locals {\n  p = file(\"${path.module}/docs/sub/policy.json\")\n}\n",
		"mod/docs/guide.md": "# guide 2\n",
	})
	r := diffAffected(t, writeGitlinkTree(t, read, []string{"mod/docs/sub"}, false), doc, "mod")
	if r.Inert || r.Fallback == nil || *r.Fallback != FallbackUnparsed {
		t.Errorf("inert %v fallback %v, want not inert, unparsed", r.Inert, r.Fallback)
	}
}

// TestMarkGitlinkShortLines: an empty or one-character patch line is no
// gitlink line and does not stop the scan.
func TestMarkGitlinkShortLines(t *testing.T) {
	t.Parallel()
	c := fileChange{patch: []byte("@@ -1 +1 @@\n\n-\n+\n-Subproject commit " + gitSHA + "\n")}
	c.markGitlink()
	if !c.oldGit || c.newGit {
		t.Errorf("old %v new %v, want true false", c.oldGit, c.newGit)
	}
}

// TestDiffGitlinkRenameAndTypeChange: the gitlink forms a diff shows with no
// hunk or as two entries of one path are changes of the gitlink, not errors.
func TestDiffGitlinkRenameAndTypeChange(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	unifiedSub := func(del, add string) string {
		return "diff --git a/docs/sub b/docs/sub\n" + del + "--- a/docs/sub\n+++ /dev/null\n@@ -1 +0,0 @@\n-" + sub + "\n" +
			"diff --git a/docs/sub b/docs/sub\n" + add
	}
	cases := map[string]struct {
		files   map[string]string
		dirs    []string
		diff    string
		changed []string // gitlinks the diff changes
	}{
		"files list, renamed, no patch": {gitlinkBase, []string{"docs/theme2"},
			`[{"filename":"docs/theme2","previous_filename":"docs/theme","status":"renamed","additions":0,"deletions":0}]`,
			[]string{"docs/theme", "docs/theme2"}},
		"unified, pure rename": {gitlinkBase, []string{"docs/theme2"},
			"diff --git a/docs/theme b/docs/theme2\nsimilarity index 100%\nrename from docs/theme\nrename to docs/theme2\n",
			[]string{"docs/theme", "docs/theme2"}},
		"files list, modified, no patch": {gitlinkBase, []string{"docs/sub"},
			`[{"filename":"docs/sub","status":"modified","additions":0,"deletions":0}]`, []string{"docs/sub"}},
		"unified, gitlink to a link": {with(gitlinkBase, map[string]string{"docs/sub": "->guide.md"}), nil,
			unifiedSub("deleted file mode 160000\n", "new file mode 120000\n--- /dev/null\n+++ b/docs/sub\n@@ -0,0 +1 @@\n+guide.md\n\\ No newline at end of file\n"),
			[]string{"docs/sub"}},
		"unified, file to a gitlink": {gitlinkBase, []string{"docs/sub"},
			"diff --git a/docs/sub b/docs/sub\ndeleted file mode 100644\n--- a/docs/sub\n+++ /dev/null\n@@ -1 +0,0 @@\n-text\n" +
				"diff --git a/docs/sub b/docs/sub\nnew file mode 160000\n--- /dev/null\n+++ b/docs/sub\n@@ -0,0 +1 @@\n+" + sub + "\n",
			[]string{"docs/sub"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			head := writeGitlinkTree(t, c.files, c.dirs, false)
			r := diffAffected(t, head, c.diff, "")
			notInert(t, r, c.changed[len(c.changed)-1], true)
			for _, p := range c.changed {
				if !slices.Contains(r.Changed, p) {
					t.Errorf("changed = %q, want %q in it", r.Changed, p)
				}
			}
		})
	}
	t.Run("the base of a gitlink that became a link holds no file", func(t *testing.T) {
		t.Parallel()
		c := cases["unified, gitlink to a link"]
		f := filepath.Join(t.TempDir(), "change")
		if err := os.WriteFile(f, []byte(c.diff), 0o600); err != nil {
			t.Fatal(err)
		}
		base, _, err := ReadDiffBase(mustTree(t, writeGitlinkTree(t, c.files, c.dirs, false)), f, DefaultLimits)
		if err != nil || base.Entries["docs/sub"] != nil {
			t.Errorf("base entry %v, err %v, want none", base.Entries["docs/sub"], err)
		}
	})
	t.Run("the base of a file that became a gitlink is unknown", func(t *testing.T) {
		t.Parallel()
		c := cases["unified, file to a gitlink"]
		f := filepath.Join(t.TempDir(), "change")
		if err := os.WriteFile(f, []byte(c.diff), 0o600); err != nil {
			t.Fatal(err)
		}
		base, _, err := ReadDiffBase(mustTree(t, writeGitlinkTree(t, c.files, c.dirs, false)), f, DefaultLimits)
		if err != nil || base.Entries["docs/sub"] == nil || base.Entries["docs/sub"].Kind != KindNoPatch {
			t.Errorf("base entry %v, err %v, want a no_patch entry", base.Entries["docs/sub"], err)
		}
	})
	t.Run("two changes of one ordinary file are still an error", func(t *testing.T) {
		t.Parallel()
		two := "diff --git a/docs/guide.md b/docs/guide.md\n--- a/docs/guide.md\n+++ b/docs/guide.md\n@@ -1 +1 @@\n-# guide\n+# guide\n" +
			"diff --git a/docs/guide.md b/docs/guide.md\n--- a/docs/guide.md\n+++ b/docs/guide.md\n@@ -1 +1 @@\n-# guide\n+# guide\n"
		f := filepath.Join(t.TempDir(), "change")
		if err := os.WriteFile(f, []byte(two), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadDiffBase(mustTree(t, writeGitlinkTree(t, gitlinkBase, nil, false)), f, DefaultLimits); err == nil {
			t.Error("want an error")
		}
	})
}

// TestAffectedModuleCallThroughChangedLink: a module call through a link
// that the change retargets puts the link in scope, outside the module root.
func TestAffectedModuleCallThroughChangedLink(t *testing.T) {
	t.Parallel()
	files := gitlinkRootFiles(map[string]string{
		"mod/main.tf":   "module \"v\" {\n  source = \"../vendor/lnk\"\n}\n",
		"vendor/real/a": "a\n",
		"vendor/real/b": "b\n",
		"vendor/lnk":    "->real",
	})
	base := writeGitlinkTree(t, with(files, map[string]string{"vendor/lnk": "->other"}), nil, false)
	head := writeGitlinkTree(t, files, nil, false)
	r, err := Affected(AffectedOptions{Head: head, Base: base, ModuleRoot: "mod", Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if r.Inert || !slices.Contains(r.Changed, "vendor/lnk") {
		t.Errorf("inert %v changed %q, want not inert with vendor/lnk", r.Inert, r.Changed)
	}
}

// TestDiffGitlinkRenameBase: a gitlink renamed or copied has nothing at its
// new path at the base, and its old path is a changed gitlink.
func TestDiffGitlinkRenameBase(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"files list, renamed": `[{"filename":"docs/theme2","previous_filename":"docs/theme","status":"renamed","additions":0,"deletions":0}]`,
		"unified, renamed":    "diff --git a/docs/theme b/docs/theme2\nsimilarity index 100%\nrename from docs/theme\nrename to docs/theme2\n",
		"unified, copied":     "diff --git a/docs/theme b/docs/theme2\nsimilarity index 100%\ncopy from docs/theme\ncopy to docs/theme2\n",
	}
	for name, diff := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			head := mustTree(t, writeGitlinkTree(t, gitlinkBase, []string{"docs/theme2"}, false))
			f := filepath.Join(t.TempDir(), "change")
			if err := os.WriteFile(f, []byte(diff), 0o600); err != nil {
				t.Fatal(err)
			}
			base, _, err := ReadDiffBase(head, f, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if e := base.Entries["docs/theme2"]; e != nil {
				t.Errorf("base entry at the new path: %+v, want none", e)
			}
			if !base.IsGitlink("docs/theme") || !base.IsGitlink("docs/theme2") {
				t.Errorf("gitlinks of the base: theme %v theme2 %v", base.IsGitlink("docs/theme"), base.IsGitlink("docs/theme2"))
			}
		})
	}
}

// TestDiffGitlinkMergedFlags: two entries of one path merge when either shows
// a gitlink, whichever side shows it first, even for a head that holds none.
func TestDiffGitlinkMergedFlags(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	plainDel := "diff --git a/docs/sub b/docs/sub\ndeleted file mode 100644\n--- a/docs/sub\n+++ /dev/null\n@@ -1 +0,0 @@\n-text\n"
	plainAdd := "diff --git a/docs/sub b/docs/sub\nnew file mode 100644\n--- /dev/null\n+++ b/docs/sub\n@@ -0,0 +1 @@\n+text\n"
	gitDel := "diff --git a/docs/sub b/docs/sub\ndeleted file mode 160000\n--- a/docs/sub\n+++ /dev/null\n@@ -1 +0,0 @@\n-" + sub + "\n"
	gitAdd := "diff --git a/docs/sub b/docs/sub\nnew file mode 160000\n--- /dev/null\n+++ b/docs/sub\n@@ -0,0 +1 @@\n+" + sub + "\n"
	cases := map[string]struct {
		diff     string
		want     string // the kind of the base entry, "" for none
		headFile bool   // the head holds a file there, not nothing
	}{
		"the first shows a gitlink at the head":     {gitAdd + plainDel, KindNoPatch, false},
		"the second shows a gitlink at the base":    {plainAdd + gitDel, "", true},
		"the second shows a gitlink at the head":    {plainDel + gitAdd, KindNoPatch, false},
		"the first shows a gitlink at the base":     {gitDel + plainAdd, "", true},
		"three entries, the first shows a gitlink":  {gitAdd + plainDel + plainDel, KindNoPatch, false},
		"three entries, the second shows a gitlink": {plainDel + gitAdd + plainDel, KindNoPatch, false},
		"three entries, a base gitlink then two":    {gitDel + plainAdd + plainAdd, "", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := gitlinkBase
			if c.headFile {
				files = with(files, map[string]string{"docs/sub": "text\n"})
			}
			head := mustTree(t, writeGitlinkTree(t, files, nil, false)) // the head holds no gitlink there
			f := filepath.Join(t.TempDir(), "change")
			if err := os.WriteFile(f, []byte(c.diff), 0o600); err != nil {
				t.Fatal(err)
			}
			base, _, err := ReadDiffBase(head, f, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if e := base.Entries["docs/sub"]; e != nil {
				got = e.Kind
			}
			if got != c.want {
				t.Errorf("base entry kind %q, want %q", got, c.want)
			}
			if gl := ChangedGitlinks(head, base); !slices.Equal(gl, []string{"docs/sub"}) {
				t.Errorf("changed gitlinks %q", gl)
			}
		})
	}
}

// TestGitlinkCallSources: only a local source names a gitlink; a registry
// address that spells the path does not, and a call in a tree with no change,
// to a gitlink only the head or only the base holds, is not read either.
func TestGitlinkCallSources(t *testing.T) {
	t.Parallel()
	status := func(t *testing.T, r *Report) string {
		t.Helper()
		return statusOf(r.Manifest, "coverage.unknown-argument", ".")
	}
	t.Run("a registry address is no local path", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{"main.tf": "module \"s\" {\n  source = \"hashicorp/consul/aws\"\n  foo = 1\n}\n"}
		r := lintGitlink(t, files, "hashicorp/consul/aws", false)
		if s := status(t, r); strings.HasPrefix(s, StatusNotEvaluated) && strings.Contains(s, "commit pointer") {
			t.Errorf("status %q", s)
		}
	})
	files := map[string]string{"main.tf": "module \"s\" {\n  source = \"./sub\"\n  foo = 1\n}\n"}
	t.Run("no change given", func(t *testing.T) {
		t.Parallel()
		r, err := Run(Options{Head: writeGitlinkTree(t, files, []string{"sub"}, false), ProfileDir: testProfile, Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if s := status(t, r); !strings.HasPrefix(s, StatusNotEvaluated) {
			t.Errorf("status %q, want not_evaluated", s)
		}
	})
	t.Run("a gitlink the diff removes", func(t *testing.T) {
		t.Parallel()
		f := filepath.Join(t.TempDir(), "change")
		patch := "@@ -1 +0,0 @@\\n-Subproject commit " + gitSHA
		if err := os.WriteFile(f, []byte(filesList("removed", "sub", patch)), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Run(Options{Head: writeGitlinkTree(t, files, nil, false), Diff: f, ProfileDir: testProfile, Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if s := status(t, r); !strings.HasPrefix(s, StatusNotEvaluated) {
			t.Errorf("status %q, want not_evaluated", s)
		}
	})
	t.Run("a rule on paths is still evaluated", func(t *testing.T) {
		t.Parallel()
		r := lintGitlink(t, files, "sub", false)
		for _, rule := range Rules {
			if rule.Class == ClassConfig || !slices.Contains(rule.Kinds, AreaRoot) || rule.CrossArea {
				continue
			}
			if s := statusOf(r.Manifest, rule.ID, "."); strings.Contains(s, "commit pointer") {
				t.Errorf("%s: %s", rule.ID, s)
			}
		}
	})
}
