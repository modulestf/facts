package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitCorpusHead is the head tree of testdata/diff-git-corpus, as its
// generate.sh leaves the work tree; executable files are marked with a
// leading "x:". sub is a gitlink.
var gitCorpusHead = map[string]string{
	"main.tf": "a = 1\nb = 20\nc = 3\n",
	"m.tf":    "x:m\n",
	"n.tf":    "",
	"r2.tf":   "x\n",
	"c.tf":    "c\n",
	"c2.tf":   "c\n",
	"q2.tf":   "x:q\n",
	"w2.tf":   "w1\nw2\nw3\nw4\nw5x\n",
	"link.tf": "->b.tf",
	"bin.dat": "\x00\x01new\n",
}

// gitCorpusBase is the base each corpus diff rebuilds: content, noPatch,
// or a leading "x:" for an executable file.
var gitCorpusBase = map[string]string{
	"main.tf": "a = 1\nb = 2\nc = 3\n",
	"m.tf":    "m\n",
	"e.tf":    "",
	"r.tf":    "x\n",
	"c.tf":    "c\n",
	"q.tf":    "q\n",
	"w.tf":    "w1\nw2\nw3\nw4\nw5\n",
	"link.tf": noPatch,
	"bin.dat": noPatch,
}

func gitCorpusTree(t *testing.T) *Tree {
	t.Helper()
	files := map[string]string{}
	for p, v := range gitCorpusHead {
		files[p] = strings.TrimPrefix(v, "x:")
	}
	root := writeGitlinkTree(t, files, []string{"sub"}, true)
	for p, v := range gitCorpusHead {
		if strings.HasPrefix(v, "x:") {
			if err := os.Chmod(filepath.Join(root, p), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return mustTree(t, root)
}

// TestUnifiedGitCorpus rebuilds the base from each diff real git wrote for
// one change (generate.sh): a mode change, an empty file removed and added,
// renames and a copy at 100%, a rename with a mode change, a rename with an
// edit, a link retarget, a gitlink bump and a binary file, with renames and
// without, with --binary, -U0 and --full-index, and as git format-patch
// writes it. Each must give the base.
func TestUnifiedGitCorpus(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "testdata", "diff-git-corpus")
	names, err := filepath.Glob(filepath.Join(dir, "*.diff"))
	if err != nil || len(names) != 6 {
		t.Fatalf("corpus %v, %v", names, err)
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			t.Parallel()
			head := gitCorpusTree(t)
			base, in, err := ReadDiffBase(head, name, DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if in.Format != DiffFormatUnified {
				t.Errorf("format %q", in.Format)
			}
			want := make([]string, 0, len(gitCorpusBase))
			for p := range gitCorpusBase {
				want = append(want, p)
			}
			slices.Sort(want)
			if !slices.Equal(base.Paths, want) {
				t.Fatalf("paths %v, want %v", base.Paths, want)
			}
			for p, w := range gitCorpusBase {
				e := base.Entries[p]
				if w == noPatch {
					if e.Kind != KindNoPatch {
						t.Errorf("%s: kind %s, want %s", p, e.Kind, KindNoPatch)
					}
					continue
				}
				if e.Kind != KindFile || string(e.Data) != strings.TrimPrefix(w, "x:") || e.Executable() != strings.HasPrefix(w, "x:") {
					t.Errorf("%s: kind %s, data %q, exec %v, want %q", p, e.Kind, e.Data, e.Executable(), w)
				}
			}
			if !base.IsGitlink("sub") || !slices.Contains(ChangedGitlinks(head, base), "sub") {
				t.Errorf("sub: not a changed gitlink")
			}
		})
	}
}

// TestUnifiedGrammarRefused: every line, order or combination outside the
// extended header grammar git writes refuses the whole input.
func TestUnifiedGrammarRefused(t *testing.T) {
	t.Parallel()
	const hunk = "--- a/main.tf\n+++ b/main.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n"
	const g = "diff --git a/main.tf b/main.tf\n"
	head := map[string]string{"main.tf": "a = 1\n", "x.tf": "->main.tf"}
	for name, c := range map[string]struct{ diff, err string }{
		"a new mode git does not write":               {g + "old mode 100644\nnew mode 100664\n", `mode "100664"`},
		"a new file mode git does not write":          {g + "new file mode 100600\n", `mode "100600"`},
		"a deleted mode git does not write":           {"diff --git a/o.tf b/o.tf\ndeleted file mode 100600\n", `mode "100600"`},
		"deleted file mode after new file":            {g + "new file mode 100644\ndeleted file mode 100644\n", "out of git's header order"},
		"similarity twice":                            {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nsimilarity index 100%\nrename from o.tf\nrename to main.tf\n", "out of git's header order"},
		"rename twice":                                {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from o.tf\nrename to main.tf\nrename from o.tf\nrename to main.tf\n", "out of git's header order"},
		"index twice":                                 {g + "index 1111111..2222222\nindex 1111111..2222222\n" + hunk, "out of git's header order"},
		"binary marker after hunks":                   {g + hunk + "Binary files a/main.tf and b/main.tf differ\n", "out of git's header order"},
		"binary patch after a binary marker":          {g + "Binary files a/main.tf and b/main.tf differ\nGIT binary patch\nliteral 0\n", "out of git's header order"},
		"index mode beside new file mode":             {"diff --git a/n.tf b/n.tf\nnew file mode 100644\nindex 0000000..2222222 100644\n", "beside a mode line"},
		"index mode beside deleted file mode":         {"diff --git a/o.tf b/o.tf\ndeleted file mode 100644\nindex 1111111..0000000 100644\n", "beside a mode line"},
		"+++ of another path":                         {g + "--- a/main.tf\n+++ b/o.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "name other paths"},
		"+++ with no ---":                             {"notes\n--- x\nmore\n+++ b/main.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "no file section"},
		"plain ---/+++ of empty paths":                {"--- a/\n+++ b/\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "a file with no path"},
		"a mode for a path the head lacks":            {"diff --git a/o.tf b/o.tf\nindex 1111111..2222222 100644\n--- a/o.tf\n+++ b/o.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "not the head entry's"},
		"old and new mode the same":                   {g + "old mode 100644\nnew mode 100644\n", "old and new mode are the same"},
		"new mode alone":                              {g + "new mode 100755\n", "not a git header line"},
		"old mode alone":                              {g + "old mode 100644\n", "old mode with no new mode"},
		"old mode before another line":                {g + "old mode 100644\nindex 1111111..2222222\n" + hunk, "old mode with no new mode"},
		"new mode not the head's":                     {g + "old mode 100644\nnew mode 100755\n", "not the head entry's"},
		"index mode not the head's":                   {g + "index 1111111..2222222 100755\n" + hunk, "not the head entry's"},
		"a file mode on a link":                       {"diff --git a/x.tf b/x.tf\nold mode 100755\nnew mode 100644\n", "not the head entry's"},
		"a link mode on a file":                       {g + "index 1111111..2222222 120000\n" + hunk, "not the head entry's"},
		"a gitlink mode on a file":                    {g + "index 1111111..2222222 160000\n" + hunk, "not the head entry's"},
		"a mode git does not write":                   {g + "old mode 100664\nnew mode 100644\n", `mode "100664"`},
		"an index mode git does not write":            {g + "index 1111111..2222222 100600\n" + hunk, `mode "100600"`},
		"index mode beside a mode line":               {g + "old mode 100755\nnew mode 100644\nindex 1111111..2222222 100644\n" + hunk, "beside a mode line"},
		"a short index hash":                          {g + "index abc..def 100644\n" + hunk, "index line"},
		"index with extra fields":                     {g + "index 1111111..2222222 100644 x\n" + hunk, "index line"},
		"new file mode twice":                         {g + "new file mode 100644\nnew file mode 100644\n", "out of git's header order"},
		"index before a mode line":                    {g + "index 1111111..2222222\nold mode 100755\nnew mode 100644\n", "out of git's header order"},
		"rename before similarity":                    {"diff --git a/o.tf b/main.tf\nrename from o.tf\nrename to main.tf\nsimilarity index 100%\n", "with no similarity index"},
		"rename with no similarity":                   {"diff --git a/o.tf b/main.tf\nrename from o.tf\nrename to main.tf\n", "with no similarity index"},
		"rename from with no rename to":               {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from o.tf\n", "no rename to"},
		"rename from to a copy to":                    {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from o.tf\ncopy to main.tf\n", "no rename to"},
		"rename from an empty path":                   {"diff --git a/ b/main.tf\nsimilarity index 100%\nrename from \nrename to main.tf\n", `rename from "" to "main.tf"`},
		"rename to an empty path":                     {"diff --git a/main.tf b/\nsimilarity index 100%\nrename from main.tf\nrename to \n", `rename from "main.tf" to ""`},
		"rename paths not the header's":               {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from p.tf\nrename to main.tf\n", `rename from "p.tf" to "main.tf"`},
		"rename of an added file":                     {g + "new file mode 100644\nsimilarity index 100%\nrename from o.tf\nrename to main.tf\n", "with no similarity index"},
		"similarity with no rename":                   {g + "similarity index 90%\nindex 1111111..2222222 100644\n" + hunk, "with no rename or copy"},
		"similarity over 100%":                        {"diff --git a/o.tf b/main.tf\nsimilarity index 101%\nrename from o.tf\nrename to main.tf\n", "over 100%"},
		"dissimilarity of an added file":              {g + "new file mode 100644\ndissimilarity index 50%\n--- /dev/null\n+++ b/main.tf\n@@ -0,0 +1 @@\n+a = 1\n", "dissimilarity of an added"},
		"headers with differing paths":                {"diff --git a/o.tf b/main.tf\nindex 1111111..2222222 100644\n--- a/o.tf\n+++ b/main.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "name other paths"},
		"differing header paths, a mode change alone": {"diff --git a/o.tf b/main.tf\nold mode 100755\nnew mode 100644\n", "no path"},
		"---/+++ of another path":                     {g + "index 1111111..2222222 100644\n--- a/o.tf\n+++ b/main.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "name other paths"},
		"---/+++ not /dev/null":                       {g + "new file mode 100644\n--- a/main.tf\n+++ b/main.tf\n@@ -0,0 +1 @@\n+a = 1\n", "name other paths"},
		"---/+++ with no hunk":                        {g + "index 1111111..2222222 100644\n--- a/main.tf\n+++ b/main.tf\n", "with no hunk"},
		"a line after the hunks":                      {g + hunk + "trailing text\n", "not a git header line"},
		"a second ---/+++ pair":                       {g + hunk + hunk, "out of git's header order"},
		"--- with no +++":                             {g + "--- a/main.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "not a git header line"},
		"binary marker of other paths":                {g + "index 1111111..2222222 100644\nBinary files a/o.tf and b/main.tf differ\n", "binary marker"},
		"binary marker then a hunk":                   {g + "Binary files a/main.tf and b/main.tf differ\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", "not a git header line"},
		"a binary patch line of spaces":               {g + "GIT binary patch\nliteral 1\nnot base85\n", "binary patch line"},
		"an unknown header line":                      {g + "mode change 100644\n" + hunk, "not a git header line"},
		"a git section after a plain one":             {hunk + g + "index 1111111..2222222 100644\n" + hunk, "not part of a file section"},
		"a stray line between plain sections":         {hunk + "Only in a: x\n" + hunk, "not part of a file section"},
		"text with --- and no +++":                    {"notes\n--- a/main.tf\nmore\n", "no file section"},
		"--- alone":                                   {"--- a/main.tf\n", "no file section"},
		"a preamble alone":                            {"From: someone\n\n--- \n", "neither"},
		"plain ---/+++ of /dev/null twice":            {"--- /dev/null\n+++ /dev/null\n@@ -0,0 +1 @@\n+a\n", "a file with no path"},
		"a mode pair of a link and a file":            {g + "old mode 120000\nnew mode 100644\n", "of two types"},
		"a mode pair of a file and a link":            {"diff --git a/x.tf b/x.tf\nold mode 100644\nnew mode 120000\n", "of two types"},
		"a mode pair of a gitlink and a file":         {g + "old mode 160000\nnew mode 100644\n" + hunk, "of two types"},
		"a mode pair of a file and a gitlink":         {g + "old mode 100644\nnew mode 160000\n", "of two types"},
		"a rename below 100% with no hunk":            {"diff --git a/o.tf b/main.tf\nsimilarity index 50%\nrename from o.tf\nrename to main.tf\nindex 1111111..2222222 100644\n", "with no hunk"},
		"a mode pair and index with no hunk":          {g + "old mode 100755\nnew mode 100644\nindex 1111111..2222222\n", "with no hunk"},
		"an index alone with no hunk":                 {g + "index 1111111..2222222 100644\n", "no hunk"},
		"a removal of a blob with no hunk":            {"diff --git a/o.tf b/o.tf\ndeleted file mode 100644\nindex 1111111..0000000\n", "with no hunk"},
		"an addition of a blob with no hunk":          {"diff --git a/n.tf b/n.tf\nnew file mode 100644\nindex 0000000..2222222\n", "with no hunk"},
		"the empty blob of a modified file":           {g + "index e69de29..0000000 100644\n", "no hunk"},
		"a link removed with no hunk":                 {"diff --git a/o.tf b/o.tf\ndeleted file mode 120000\nindex e69de29..0000000\n", "with no hunk"},
		"a new file mode not the head's":              {g + "new file mode 100755\nindex 0000000..2222222\n--- /dev/null\n+++ b/main.tf\n@@ -0,0 +1 @@\n+a = 1\n", "not the head entry's"},
		"a link added over a head file":               {g + "new file mode 120000\nindex 0000000..2222222\n--- /dev/null\n+++ b/main.tf\n@@ -0,0 +1 @@\n+a = 1\n", "not the head entry's"},
		"a signature before the end":                  {g + hunk + "-- \n2.56.1\n" + g + "index 1111111..2222222\n" + hunk, "not a git header line"},
		"a change of type as git writes it":           {g + "deleted file mode 120000\nindex 1111111..0000000\n--- a/main.tf\n+++ /dev/null\n@@ -1 +0,0 @@\n-x.tf\n\\ No newline at end of file\n" + g + "new file mode 100644\nindex 0000000..2222222\n--- /dev/null\n+++ b/main.tf\n@@ -0,0 +1 @@\n+a = 1\n", "removed by the diff"},
		"a removal with no hunk and no index":         {"diff --git a/o.tf b/o.tf\ndeleted file mode 100644\n", "with no hunk"},
		"a removal with no hunk and equal hashes":     {"diff --git a/o.tf b/o.tf\ndeleted file mode 100644\nindex 1111111..1111111\n", "with no hunk"},
		"an addition with no hunk and no index":       {"diff --git a/n.tf b/n.tf\nnew file mode 100644\n", "with no hunk"},
		"a rename below 100% with no index":           {"diff --git a/o.tf b/main.tf\nsimilarity index 50%\nrename from o.tf\nrename to main.tf\n", "with no hunk"},
		"a rename below 100% with equal hashes":       {"diff --git a/o.tf b/main.tf\nsimilarity index 50%\nrename from o.tf\nrename to main.tf\nindex 1111111..1111111 100644\n", "with no hunk"},
		"a copy below 100% with no hunk":              {"diff --git a/o.tf b/main.tf\nsimilarity index 50%\ncopy from o.tf\ncopy to main.tf\n", "with no hunk"},
		"a rename at 100% with differing hashes":      {"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from o.tf\nrename to main.tf\nindex 1111111..2222222 100644\n", "with no hunk"},
		"a dissimilarity index with no hunk":          {g + "old mode 100755\nnew mode 100644\ndissimilarity index 60%\n", "dissimilarity index with no hunk"},
		"a version line with no signature line":       {g + hunk + "Signed-off: x\n2.56.1\n", "not a git header line"},
		"a signature with no version":                 {g + hunk + "-- \nhello\n", "not a git header line"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := mustTree(t, writeTree(t, head))
			if _, _, err := baseFromDiff(tr, []byte(c.diff), DefaultLimits); err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("error %v, want %q", err, c.err)
			}
		})
	}
}

// TestUnifiedGrammarAccepted: the shapes the grammar takes beyond the
// corpus: a preamble, a plain diff, trailing blank lines, a gitlink bump on
// a checkout with no submodule and blank input.
func TestUnifiedGrammarAccepted(t *testing.T) {
	t.Parallel()
	const hunk = "@@ -1 +1 @@\n-a = 0\n+a = 1\n"
	head := map[string]string{"main.tf": "a = 1\n"}
	for name, c := range map[string]struct {
		diff string
		want string // the base main.tf
	}{
		"a mail preamble":            {"From: someone\nSubject: x\n\n---\n main.tf | 2 +-\n\ndiff --git a/main.tf b/main.tf\nindex 1111111..2222222 100644\n--- a/main.tf\n+++ b/main.tf\n" + hunk, "a = 0\n"},
		"a plain diff":               {"--- a/main.tf\t2026-01-01\n+++ b/main.tf\t2026-01-02\n" + hunk, "a = 0\n"},
		"trailing blank lines":       {"diff --git a/main.tf b/main.tf\n--- a/main.tf\n+++ b/main.tf\n" + hunk + "\n\n", "a = 0\n"},
		"blank input":                {"\n", "a = 1\n"},
		"a format-patch signature":   {"From 1 Mon Sep 17 00:00:00 2001\nSubject: x\n\n---\n main.tf | 2 +-\n\ndiff --git a/main.tf b/main.tf\nindex 1111111..2222222 100644\n--- a/main.tf\n+++ b/main.tf\n" + hunk + "-- \n2.39.5 (Apple Git-154)\n\n", "a = 0\n"},
		"dissimilarity of a rewrite": {"diff --git a/main.tf b/main.tf\ndissimilarity index 100%\nindex 1111111..2222222 100644\n--- a/main.tf\n+++ b/main.tf\n" + hunk, "a = 0\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, _, err := baseFromDiff(mustTree(t, writeTree(t, head)), []byte(c.diff), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if e := got.Entries["main.tf"]; e == nil || string(e.Data) != c.want {
				t.Errorf("main.tf %+v, want %q", e, c.want)
			}
		})
	}
	// Sections git wrote (git 2.56.1, core.quotePath=true): paths holding a
	// space and " b/", binary files added, removed and renamed with no
	// --binary. Each maps a base path to its want: content, noPatch or
	// absent.
	for name, c := range map[string]struct {
		head map[string]string
		diff string
		want map[string]string
	}{
		"a path with a space": {map[string]string{"my file.tf": "x = 2\n"},
			"diff --git a/my file.tf b/my file.tf\nindex 7d4290a..407de30 100644\n--- a/my file.tf\t\n+++ b/my file.tf\t\n@@ -1 +1 @@\n-x = 1\n+x = 2\n",
			map[string]string{"my file.tf": "x = 1\n"}},
		"a path holding b/": {map[string]string{"a b/c.tf": "y = 2\n"},
			"diff --git a/a b/c.tf b/a b/c.tf\nindex a003ef7..47643d4 100644\n--- a/a b/c.tf\t\n+++ b/a b/c.tf\t\n@@ -1 +1 @@\n-y = 1\n+y = 2\n",
			map[string]string{"a b/c.tf": "y = 1\n"}},
		"a rename at 100% with equal hashes": {map[string]string{"main.tf": "a = 1\n"},
			"diff --git a/o.tf b/main.tf\nsimilarity index 100%\nrename from o.tf\nrename to main.tf\nindex 1111111..1111111 100644\n",
			map[string]string{"o.tf": "a = 1\n", "main.tf": ""}},
		"a binary file added": {map[string]string{"add.dat": "\x00new\n"},
			"diff --git a/add.dat b/add.dat\nnew file mode 100644\nindex 0000000..843f4dc\nBinary files /dev/null and b/add.dat differ\n",
			map[string]string{"add.dat": ""}},
		"a binary file removed": {map[string]string{},
			"diff --git a/gone.dat b/gone.dat\ndeleted file mode 100644\nindex 3c4f499..0000000\nBinary files a/gone.dat and /dev/null differ\n",
			map[string]string{"gone.dat": noPatch}},
		"a binary file renamed": {map[string]string{"new.dat": "\x00b\n"},
			"diff --git a/old.dat b/new.dat\nsimilarity index 98%\nrename from old.dat\nrename to new.dat\nindex 9304e6e..8a6ac19 100644\nBinary files a/old.dat and b/new.dat differ\n",
			map[string]string{"old.dat": noPatch, "new.dat": ""}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, _, err := baseFromDiff(mustTree(t, writeTree(t, c.head)), []byte(c.diff), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			for p, w := range c.want {
				e := got.Entries[p]
				switch {
				case w == "":
					if e != nil {
						t.Errorf("%s: %+v, want absent", p, e)
					}
				case w == noPatch:
					if e == nil || e.Kind != KindNoPatch {
						t.Errorf("%s: %+v, want %s", p, e, KindNoPatch)
					}
				case e == nil || string(e.Data) != w:
					t.Errorf("%s: %+v, want %q", p, e, w)
				}
			}
		})
	}
	t.Run("a plain removal", func(t *testing.T) {
		t.Parallel()
		got, _, err := baseFromDiff(mustTree(t, writeTree(t, head)), []byte("--- a/gone.tf\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n"), DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		if e := got.Entries["gone.tf"]; e == nil || string(e.Data) != "x\n" {
			t.Errorf("gone.tf %+v", e)
		}
	})
	t.Run("a binary patch at the end", func(t *testing.T) {
		t.Parallel()
		diff := "diff --git a/main.tf b/main.tf\nindex 1111111..2222222 100644\nGIT binary patch\nliteral 1\nIcmZ?d00001\n\nliteral 1\nIcmZ?d00001"
		got, _, err := baseFromDiff(mustTree(t, writeTree(t, head)), []byte(diff), DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		if e := got.Entries["main.tf"]; e == nil || e.Kind != KindNoPatch {
			t.Errorf("main.tf %+v", e)
		}
	})
	t.Run("an exec bit change of an oversize head file", func(t *testing.T) {
		t.Parallel()
		big := strings.Repeat("a = 1\n", 500000) // 3,000,000 bytes, over the 2 MiB cap
		head := mustTree(t, writeTree(t, map[string]string{"big.tf": big}))
		if h := head.Entries["big.tf"]; h == nil || h.Kind != KindOversize || h.Executable() {
			t.Fatalf("head big.tf %+v", h)
		}
		for name, c := range map[string]struct {
			diff string
			kind string
		}{
			"a mode pair alone":      {"diff --git a/big.tf b/big.tf\nold mode 100755\nnew mode 100644\n", KindOversize},
			"a mode pair and a hunk": {"diff --git a/big.tf b/big.tf\nold mode 100755\nnew mode 100644\nindex 1111111..2222222\n--- a/big.tf\n+++ b/big.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", KindNoPatch},
			"a hunk alone":           {"diff --git a/big.tf b/big.tf\nindex 1111111..2222222 100644\n--- a/big.tf\n+++ b/big.tf\n@@ -1 +1 @@\n-a = 0\n+a = 1\n", KindNoPatch},
		} {
			got, _, err := baseFromDiff(head, []byte(c.diff), DefaultLimits)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			e := got.Entries["big.tf"]
			if e == nil || e.Kind != c.kind || sameEntry(head.Entries["big.tf"], e) {
				t.Errorf("%s: base big.tf %+v, want %s and changed", name, e, c.kind)
			}
			if c.kind == KindOversize && !e.Executable() {
				t.Errorf("%s: base big.tf not executable", name)
			}
		}
	})
	t.Run("a mode of an oversize head file", func(t *testing.T) {
		t.Parallel()
		lim := DefaultLimits
		lim.MaxFileBytes = 4
		tr, err := ReadTree(writeTree(t, map[string]string{"big.tf": "aaaaaaaa\n"}), lim)
		if err != nil {
			t.Fatal(err)
		}
		diff := "diff --git a/big.tf b/big.tf\nindex 1111111..2222222 100644\n--- a/big.tf\n+++ b/big.tf\n@@ -1 +1 @@\n-x\n+aaaaaaaa\n"
		got, _, err := baseFromDiff(tr, []byte(diff), lim)
		if err != nil {
			t.Fatal(err)
		}
		if e := got.Entries["big.tf"]; e == nil || e.Kind != KindNoPatch {
			t.Errorf("big.tf %+v", e)
		}
	})
	t.Run("a gitlink bump with no submodule checked out", func(t *testing.T) {
		t.Parallel()
		tr := mustTree(t, writeGitlinkTree(t, head, []string{"sub"}, false))
		diff := "diff --git a/sub b/sub\nindex 1111111..2222222 160000\n--- a/sub\n+++ b/sub\n@@ -1 +1 @@\n-Subproject commit 1111111111111111111111111111111111111111\n+Subproject commit 2222222222222222222222222222222222222222\n"
		got, _, err := baseFromDiff(tr, []byte(diff), DefaultLimits)
		if err != nil || !slices.Contains(ChangedGitlinks(tr, got), "sub") {
			t.Fatalf("error %v", err)
		}
	})
}
