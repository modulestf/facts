package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkCallFiles is a root whose module call goes through the link lnk. The
// head adds the argument foo, so every form of the change touches the root.
func linkCallFiles(source string, head bool) map[string]string {
	call := "module \"s\" {\n  source = \"" + source + "\"\n"
	if head {
		call += "  foo = 1\n"
	}
	return map[string]string{
		"main.tf":            call + "}\n",
		"real/main.tf":       "variable \"foo\" {\n  type = number\n}\n",
		"real/inner/main.tf": "variable \"foo\" {\n  type = number\n}\n",
	}
}

// linkCallTree writes the files, the gitlink sub and the link lnk to target.
func linkCallTree(t *testing.T, files map[string]string, target string) string {
	t.Helper()
	root := writeGitlinkTree(t, files, []string{"sub"}, false)
	if err := os.Symlink(target, filepath.Join(root, "lnk")); err != nil {
		t.Fatal(err)
	}
	return root
}

// linkCallChange is the change that adds foo to the call, in a form.
func linkCallChange(form, source string) string {
	q := `\"`
	switch form {
	case "files list":
		return filesList("modified", "main.tf", "@@ -1,3 +1,4 @@\\n module "+q+"s"+q+" {\\n   source = "+q+source+q+"\\n+  foo = 1\\n }")
	case "unified":
		return "diff --git a/main.tf b/main.tf\nindex abc1234..def5678 100644\n--- a/main.tf\n+++ b/main.tf\n" +
			"@@ -1,3 +1,4 @@\n module \"s\" {\n   source = \"" + source + "\"\n+  foo = 1\n }\n"
	}
	panic(form)
}

// lintLinkCall runs every rule on the change in a form: two trees (--base),
// a files list or a unified diff.
func lintLinkCall(t *testing.T, form, source, target string) *Report {
	t.Helper()
	o := Options{Head: linkCallTree(t, linkCallFiles(source, true), target), ProfileDir: testProfile, Limits: DefaultLimits}
	if form == "base" {
		o.Base = linkCallTree(t, linkCallFiles(source, false), target)
	} else {
		f := filepath.Join(t.TempDir(), "change")
		if err := os.WriteFile(f, []byte(linkCallChange(form, source)), 0o600); err != nil {
			t.Fatal(err)
		}
		o.Diff = f
	}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestLinkCalleeIsNotEvaluated: a local module call whose path goes through a
// link, to a gitlink or to an ordinary directory, as the whole path or a step
// of it, leaves the configuration rules of the calling area not evaluated in
// every form of the change. The engine never follows a link, so it cannot
// read the callee.
func TestLinkCalleeIsNotEvaluated(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ source, target string }{
		"link to a gitlink":        {"./lnk", "sub"},
		"link to a directory":      {"./lnk", "real"},
		"link step to a gitlink":   {"./lnk/inner", "sub"},
		"link step to a directory": {"./lnk/inner", "real"},
	}
	for name, c := range cases {
		for _, form := range []string{"base", "files list", "unified"} {
			t.Run(name+", "+form, func(t *testing.T) {
				t.Parallel()
				r := lintLinkCall(t, form, c.source, c.target)
				for _, rule := range []string{"scope.module-call", "coverage.unknown-argument"} {
					if s := statusOf(r.Manifest, rule, "."); !strings.HasPrefix(s, StatusNotEvaluated) || !strings.Contains(s, "link") {
						t.Errorf("%s: %s, want not_evaluated for the link", rule, s)
					}
				}
			})
		}
	}
	t.Run("a call into a directory is still read", func(t *testing.T) {
		t.Parallel()
		for _, form := range []string{"base", "files list", "unified"} {
			r := lintLinkCall(t, form, "./real", "real")
			if s := statusOf(r.Manifest, "scope.module-call", "."); strings.HasPrefix(s, StatusNotEvaluated) {
				t.Errorf("%s: scope.module-call: %s", form, s)
			}
		}
	})
}

// TestMarkUnreadCalls checks each step of a call path: a link at any step
// marks the area, at head or at base, and no other entry does.
func TestMarkUnreadCalls(t *testing.T) {
	t.Parallel()
	tree := func(link bool) *Tree {
		tr := &Tree{Entries: map[string]*Entry{"m/f.tf": {Path: "m/f.tf", Kind: KindFile}}, Dirs: map[string]bool{".": true, "m": true}}
		if link {
			tr.Entries["m/l"] = &Entry{Path: "m/l", Kind: KindSymlink}
		}
		return tr
	}
	call := func(src string) *Module { return &Module{ModuleCalls: []ModuleCall{{Name: "c", Source: src}}} }
	cases := map[string]struct {
		src        string
		head, base bool // a link m/l at head, at base
		want       bool
	}{
		"link at head":        {"./l", true, false, true},
		"link at base":        {"./l", false, true, true},
		"link step":           {"./l/x", true, false, true},
		"link step at base":   {"./l/x", false, true, true},
		"no link":             {"./l", false, false, false},
		"a regular file step": {"./f.tf/x", false, false, false},
		"registry source":     {"l", true, true, false},
		"parent through link": {"../m/l", true, false, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := &Area{Path: "m", Head: call(c.src), headTree: tree(c.head), baseTree: tree(c.base)}
			a.markUnreadCalls()
			if got := a.callsUnread != ""; got != c.want {
				t.Errorf("marked %v (%q), want %v", got, a.callsUnread, c.want)
			}
		})
	}
	t.Run("only whole steps and links count", func(t *testing.T) {
		t.Parallel()
		tr := &Tree{Entries: map[string]*Entry{
			"m/l":  {Path: "m/l", Kind: KindSymlink},
			"m/o":  {Path: "m/o", Kind: KindOversize},
			"m/np": {Path: "m/np", Kind: KindNoPatch},
		}, Dirs: map[string]bool{".": true, "m": true}}
		for src, want := range map[string]bool{"./lx": false, "./o/x": false, "./np/x": false, "./l": true} {
			a := &Area{Path: "m", Head: call(src), headTree: tr}
			if a.markUnreadCalls(); (a.callsUnread != "") != want {
				t.Errorf("%s: marked %q, want %v", src, a.callsUnread, want)
			}
		}
	})
	t.Run("no base tree", func(t *testing.T) {
		t.Parallel()
		a := &Area{Path: "m", Head: call("./l"), headTree: tree(true)}
		if a.markUnreadCalls(); a.callsUnread == "" {
			t.Error("not marked")
		}
	})
}

// TestParseUnifiedModeLines checks that only an index line gives the mode of
// an index line, and that a mode the diff states wins over hunk lines.
func TestParseUnifiedModeLines(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	hunk := "--- a/s\n+++ b/s\n@@ -1 +1 @@\n-" + sub + "\n+" + sub + "1\n"
	plain := "--- a/s\n+++ b/s\n@@ -1 +1 @@\n-x\n+y\n" // git writes a hunk for every gitlink change
	cases := map[string]struct {
		diff           string
		path, prev     string
		oldGit, newGit bool
	}{
		"rename of a path ending in the mode": {"diff --git a/x/a 160000 b/x/b 160000\nsimilarity index 100%\nrename from x/a 160000\nrename to x/b 160000\n",
			"x/b 160000", "x/a 160000", false, false},
		"copy of a path ending in the mode": {"diff --git a/x/a 160000 b/x/b 160000\nsimilarity index 100%\ncopy from x/a 160000\ncopy to x/b 160000\n",
			"x/b 160000", "x/a 160000", false, false},
		"index line of a gitlink":       {"diff --git a/s b/s\nindex abc..def 160000\n" + hunk, "s", "s", true, true},
		"index line of a file":          {"diff --git a/s b/s\nindex abc..def 100644\n" + hunk, "s", "s", false, false},
		"index line with a longer mode": {"diff --git a/s b/s\nindex abc..def 1160000\n" + plain, "s", "s", false, false},
		"index line with extra fields":  {"diff --git a/s b/s\nindex abc..def 160000 x\n" + plain, "s", "s", false, false},
		"index line with no mode":       {"diff --git a/s b/s\nindex abc..def\n" + hunk, "s", "s", true, true},
		"new file of a file":            {"diff --git a/s b/s\nnew file mode 100644\n--- /dev/null\n+++ b/s\n@@ -0,0 +1 @@\n+" + sub + "\n", "s", "", false, false},
		"deleted file of a file":        {"diff --git a/s b/s\ndeleted file mode 100644\n--- a/s\n+++ /dev/null\n@@ -1 +0,0 @@\n-" + sub + "\n", "s", "", false, false},
		"mode change of a file":         {"diff --git a/s b/s\nold mode 100644\nnew mode 100755\n" + hunk, "s", "s", false, false},
		"old mode of a file alone":      {"diff --git a/s b/s\nold mode 100644\nnew mode 160000\n" + hunk, "s", "s", false, true},
		"new mode of a file alone":      {"diff --git a/s b/s\nold mode 160000\nnew mode 100644\n" + hunk, "s", "s", true, false},
		"index line after a mode line":  {"diff --git a/s b/s\ndeleted file mode 160000\nindex abc..def 100644\n", "s", "", true, false},
		"no mode line":                  {"diff --git a/s b/s\n" + hunk, "s", "s", true, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := parseUnified([]byte(c.diff))
			if err != nil || len(got) != 1 {
				t.Fatalf("parse: %v %v", got, err)
			}
			g := got[0]
			if g.path != c.path || g.prev != c.prev || g.oldGit != c.oldGit || g.newGit != c.newGit {
				t.Errorf("path %q prev %q old %v new %v, want %q %q %v %v", g.path, g.prev, g.oldGit, g.newGit, c.path, c.prev, c.oldGit, c.newGit)
			}
		})
	}
}

// TestAffectedDiffSubprojectText: a regular file whose lines read like a
// submodule commit is a file when the unified diff states its mode, and is
// still taken as a gitlink by a files list, which states none.
func TestAffectedDiffSubprojectText(t *testing.T) {
	t.Parallel()
	sub := "Subproject commit " + gitSHA
	head := writeGitlinkTree(t, with(gitlinkBase, map[string]string{"docs/note": sub + "1\n"}), nil, false)
	unified := "diff --git a/docs/note b/docs/note\nindex abc1234..def5678 100644\n--- a/docs/note\n+++ b/docs/note\n@@ -1 +1 @@\n-" + sub + "\n+" + sub + "1\n"
	r := diffAffected(t, head, unified, "")
	if r.Fallback != nil {
		t.Errorf("unified: fallback %v, want none", r.Fallback)
	}
	f := filepath.Join(t.TempDir(), "change")
	if err := os.WriteFile(f, []byte(filesList("modified", "docs/note", "@@ -1 +1 @@\\n-"+sub+"\\n+"+sub+"1")), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := Affected(AffectedOptions{Head: head, Diff: f, Limits: DefaultLimits}); err == nil && r.Fallback == nil && r.Inert {
		t.Errorf("files list: inert with no fallback, want it fail closed")
	}
}
