package facts

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const diffFixture = "../../testdata/diff-base"

// TestReadDiffBaseFixture rebuilds the base of testdata/diff-base from its
// GitHub files list and from its unified diff, and checks each equals the
// committed base tree: the same paths, kinds, contents and directories.
func TestReadDiffBaseFixture(t *testing.T) {
	head := mustTree(t, filepath.Join(diffFixture, "head"))
	want := mustTree(t, filepath.Join(diffFixture, "base"))
	for _, tc := range []struct{ file, format string }{
		{"files.json", DiffFormatGitHubFiles},
		{"change.diff", DiffFormatUnified},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got, in, err := ReadDiffBase(head, filepath.Join(diffFixture, tc.file), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			if in.Format != tc.format || len(in.SHA256) != 64 {
				t.Errorf("diff input %+v", in)
			}
			if !slices.Equal(got.Paths, want.Paths) {
				t.Errorf("paths\n got %v\nwant %v", got.Paths, want.Paths)
			}
			for _, p := range want.Paths {
				if g, w := got.Entries[p], want.Entries[p]; g == nil || g.Kind != w.Kind || string(g.Data) != string(w.Data) {
					t.Errorf("%s: got %+v, want %q", p, g, w.Data)
				}
			}
			if !maps.Equal(got.Dirs, want.Dirs) {
				t.Errorf("dirs\n got %v\nwant %v", got.Dirs, want.Dirs)
			}
			if got.TreeSHA256() != want.TreeSHA256() {
				t.Error("tree hash differs from the committed base")
			}
		})
	}
}

// ghFile is one entry of a GitHub pull request files list.
type ghFile struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename string  `json:"previous_filename,omitempty"`
	Patch            *string `json:"patch,omitempty"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
}

func ghList(t *testing.T, files ...ghFile) string {
	t.Helper()
	b, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const noPatch = "<no patch>"

// TestBaseFromDiff covers each status, the patch-less cases and every
// refusal, in both input formats.
func TestBaseFromDiff(t *testing.T) {
	t.Parallel()
	const v1 = "a = 1\nb = 2\nc = 3\n"
	const v2 = "a = 1\nb = 20\nc = 3\n"
	edit := "@@ -1,3 +1,3 @@\n a = 1\n-b = 2\n+b = 20\n c = 3"
	for _, tc := range []struct {
		name string
		head map[string]string
		diff func(t *testing.T) string
		want map[string]string // the base: content, or noPatch
		err  string
	}{
		{
			name: "modified",
			head: map[string]string{"main.tf": v2, "other.tf": "x\n"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new(edit), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"main.tf": v1, "other.tf": "x\n"},
		},
		{
			name: "added and removed",
			head: map[string]string{"new.tf": "n\n"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "new.tf", Status: "added", Patch: new("@@ -0,0 +1 @@\n+n"), Additions: 1},
					ghFile{Filename: "gone/old.tf", Status: "removed", Patch: new("@@ -1,2 +0,0 @@\n-o\n-p"), Deletions: 2})
			},
			want: map[string]string{"gone/old.tf": "o\np\n"},
		},
		{
			name: "renamed with a patch",
			head: map[string]string{"b.tf": v2},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed", Patch: new(edit), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"a.tf": v1},
		},
		{
			name: "renamed onto a path the change adds again",
			head: map[string]string{"a.tf": "fresh\n", "b.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "a.tf", Status: "added", Patch: new("@@ -0,0 +1 @@\n+fresh"), Additions: 1},
					ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed"})
			},
			want: map[string]string{"a.tf": noPatch},
		},
		{
			name: "a rename with 0 and 0 and no patch is unknown: the base may have been binary",
			head: map[string]string{"b.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed"})
			},
			want: map[string]string{"a.tf": noPatch},
		},
		{
			name: "rename of a binary file has no patch",
			head: map[string]string{"b.png": "\x89PNG\x00\x01"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.png", PreviousFilename: "a.png", Status: "renamed"})
			},
			want: map[string]string{"a.png": noPatch},
		},
		{
			name: "rename with counts and no patch",
			head: map[string]string{"b.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed", Additions: 4000, Deletions: 3})
			},
			want: map[string]string{"a.tf": noPatch},
		},
		{
			name: "modified and removed with no patch",
			head: map[string]string{"big.tf": v2},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "big.tf", Status: "modified", Additions: 9000},
					ghFile{Filename: "logo.png", Status: "removed"})
			},
			want: map[string]string{"big.tf": noPatch, "logo.png": noPatch},
		},
		{
			name: "a removed .tf file with no patch and no line is unknown",
			head: map[string]string{"main.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "setup/variables.tf", Status: "removed"})
			},
			want: map[string]string{"main.tf": v1, "setup/variables.tf": noPatch},
		},
		{
			name: "a removed .tf file with no counts and no patch is unknown",
			head: map[string]string{"main.tf": v1},
			diff: func(*testing.T) string { return `[{"filename":"variables.tf","status":"removed"}]` },
			want: map[string]string{"main.tf": v1, "variables.tf": noPatch},
		},
		{
			name: "a removed .tf file with a blank patch is unknown",
			head: map[string]string{"main.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "variables.tf", Status: "removed", Patch: new(" \n")})
			},
			want: map[string]string{"main.tf": v1, "variables.tf": noPatch},
		},
		{
			name: "a modified, changed or renamed file with no counts and no patch is unknown",
			head: map[string]string{"a.tf": v1, "b.tf": v1, "c.tf": v1},
			diff: func(*testing.T) string {
				return `[{"filename":"a.tf","status":"modified"},{"filename":"b.tf","status":"changed","additions":0},` +
					`{"filename":"c.tf","status":"renamed","previous_filename":"d.tf","deletions":0}]`
			},
			want: map[string]string{"a.tf": noPatch, "b.tf": noPatch, "d.tf": noPatch},
		},
		{
			name: "a renamed or modified file with removed lines and no patch is unknown",
			head: map[string]string{"a.tf": v1, "c.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "a.tf", Status: "modified", Deletions: 2},
					ghFile{Filename: "c.tf", PreviousFilename: "d.tf", Status: "renamed", Deletions: 2})
			},
			want: map[string]string{"a.tf": noPatch, "d.tf": noPatch},
		},
		{
			name: "a modified or changed entry with 0 and 0 and no patch is unknown",
			head: map[string]string{"a.tf": v1, "b.tf": v2},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "a.tf", Status: "modified"}, ghFile{Filename: "b.tf", Status: "changed"})
			},
			want: map[string]string{"a.tf": noPatch, "b.tf": noPatch},
		},
		{
			name: "null and negative counts with no patch are unknown",
			head: map[string]string{"a.tf": v1, "b.tf": v1},
			diff: func(*testing.T) string {
				return `[{"filename":"a.tf","status":"modified","additions":null,"deletions":null},` +
					`{"filename":"b.tf","status":"renamed","previous_filename":"c.tf","additions":-1,"deletions":-1},` +
					`{"filename":"d.tf","status":"removed","additions":0,"deletions":-5}]`
			},
			want: map[string]string{"a.tf": noPatch, "c.tf": noPatch, "d.tf": noPatch},
		},
		{
			name: "a link at head with a patch",
			head: map[string]string{"link.tf": "->other.tf", "other.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "link.tf", Status: "modified", Patch: new("@@ -1 +1 @@\n-x.tf\n+other.tf"), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"link.tf": noPatch, "other.tf": v1},
		},
		{
			name: "no newline at the end of the base",
			head: map[string]string{"main.tf": "a\nb\n"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1,2 +1,2 @@\n a\n-c\n\\ No newline at end of file\n+b"), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"main.tf": "a\nc"},
		},
		{
			name: "crlf lines",
			head: map[string]string{"main.tf": "a\r\nb\r\n"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1,2 +1,2 @@\n a\r\n-c\r\n+b\r"), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"main.tf": "a\r\nc\r\n"},
		},
		{
			name: "a path under .terraform is the change's own, as any other",
			head: map[string]string{"main.tf": v1, ".terraform/m/main.tf": v2},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: ".terraform/m/main.tf", Status: "modified", Patch: new(edit), Additions: 1, Deletions: 1})
			},
			want: map[string]string{"main.tf": v1, ".terraform/m/main.tf": v1},
		},
		{
			name: "a rename into and out of .terraform is each side's own",
			head: map[string]string{".terraform/b.tf": v1, "c.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: ".terraform/b.tf", PreviousFilename: "a.tf", Status: "renamed"},
					ghFile{Filename: "c.tf", PreviousFilename: ".terraform/c.tf", Status: "renamed"})
			},
			want: map[string]string{"a.tf": noPatch, ".terraform/c.tf": noPatch},
		},
		{
			name: "no marker file turns a .terraform path into a skipped one",
			head: map[string]string{"main.tf": v1, ".terraform/terraform.tfstate": "{}", ".terraform/modules/modules.json": "{}"},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: ".terraform/x.tf", Status: "removed", Patch: new("@@ -1 +0,0 @@\n-a")})
			},
			want: map[string]string{"main.tf": v1, ".terraform/terraform.tfstate": "{}", ".terraform/modules/modules.json": "{}", ".terraform/x.tf": "a\n"},
		},
		{
			name: "an empty list is no change",
			head: map[string]string{"main.tf": v1},
			diff: func(*testing.T) string { return " []\n" },
			want: map[string]string{"main.tf": v1},
		},
		{
			name: "unified: binary, pure rename, empty new and deleted files",
			head: map[string]string{"b.tf": v1, "empty.tf": "", "logo.png": "\x00"},
			diff: func(*testing.T) string {
				return "diff --git a/logo.png b/logo.png\nindex 1..2 100644\nBinary files a/logo.png and b/logo.png differ\n" +
					"diff --git a/a.tf b/b.tf\nsimilarity index 100%\nrename from a.tf\nrename to b.tf\n" +
					"diff --git a/empty.tf b/empty.tf\nnew file mode 100644\nindex 0000000..e69de29\n" +
					"diff --git a/gone.tf b/gone.tf\ndeleted file mode 100644\nindex e69de29..0000000\n"
			},
			want: map[string]string{"a.tf": v1, "gone.tf": "", "logo.png": noPatch},
		},
		{
			name: "unified without git headers",
			head: map[string]string{"main.tf": v2},
			diff: func(*testing.T) string {
				return "Some preamble\n--- a/main.tf\t2026-01-01\n+++ b/main.tf\t2026-01-02\n" + edit + "\n"
			},
			want: map[string]string{"main.tf": v1},
		},
		{
			name: "unified: a removed line that looks like a header",
			head: map[string]string{"main.tf": "a\n"},
			diff: func(*testing.T) string {
				return "--- a/main.tf\n+++ b/main.tf\n@@ -1,2 +1 @@\n a\n--- b\n"
			},
			want: map[string]string{"main.tf": "a\n-- b\n"},
		},
		{name: "context does not match the head", head: map[string]string{"main.tf": "a = 9\nb = 20\nc = 3\n"}, err: "does not match the head",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new(edit)})
			}},
		{name: "hunk counts do not match its lines", head: map[string]string{"main.tf": v2}, err: "hunk",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1,4 +1,3 @@\n a = 1\n-b = 2\n+b = 20\n c = 3")})
			}},
		{name: "hunks out of order", head: map[string]string{"main.tf": v2}, err: "hunk",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -3 +3 @@\n c = 3\n@@ -1 +1 @@\n a = 1")})
			}},
		{name: "hunk past the end of the head", head: map[string]string{"main.tf": "a\n"}, err: "does not match the head",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -5 +5 @@\n-x\n+y")})
			}},
		{name: "a patch that is not a hunk", head: map[string]string{"main.tf": v2}, err: "hunk",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("garbage")})
			}},
		{name: "unsafe path", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: "../main.tf", Status: "added"}) }},
		{name: "control character in a path", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: "a\nb.tf", Status: "added"}) }},
		{name: "a path listed twice", head: map[string]string{"main.tf": v2}, err: "more than once",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new(edit)}, ghFile{Filename: "main.tf", Status: "modified", Patch: new(edit)})
			}},
		{name: "a .git path", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: "a/.git/config", Status: "removed"}) }},
		{name: "a pure-rename status is no GitHub status", head: map[string]string{"b.tf": v1}, err: `files list: "b.tf": unknown status "pure-rename"`,
			diff: func(*testing.T) string {
				return `[{"filename":"b.tf","status":"pure-rename","previous_filename":"d.tf","additions":500,"deletions":500}]`
			}},
		{name: "a mode-only status is no GitHub status", head: map[string]string{"b.tf": v1}, err: `files list: "b.tf": unknown status "mode-only"`,
			diff: func(*testing.T) string { return `[{"filename":"b.tf","status":"mode-only"}]` }},
		{name: "a removed-empty status is no GitHub status", head: map[string]string{"b.tf": v1}, err: `files list: "x.tf": unknown status "removed-empty"`,
			diff: func(*testing.T) string { return `[{"filename":"x.tf","status":"removed-empty"}]` }},
		{name: "no status", head: map[string]string{"b.tf": v1}, err: `files list: "b.tf": unknown status ""`,
			diff: func(*testing.T) string { return `[{"filename":"b.tf"}]` }},
		{name: "a count that is a string", head: map[string]string{"b.tf": v1}, err: "files list",
			diff: func(*testing.T) string {
				return `[{"filename":"b.tf","status":"modified","additions":"0","deletions":0}]`
			}},
		{name: "a .GIT path", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: ".GIT/x.tf", Status: "added"}) }},
		{name: "a rename from .git", head: map[string]string{"main2.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main2.tf", PreviousFilename: ".git/x.tf", Status: "renamed"})
			}},
		{name: "a rename into .git", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "x/.git/y.tf", PreviousFilename: "main.tf", Status: "renamed"})
			}},
		{name: "unknown status", head: map[string]string{"main.tf": v1}, err: "status",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: "main.tf", Status: "exploded"}) }},
		{name: "a modified file the head lacks", head: map[string]string{"main.tf": v1}, err: "not in the head tree",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "x.tf", Status: "modified", Patch: new(edit)})
			}},
		{name: "a removed file the head has", head: map[string]string{"main.tf": v1}, err: "still in the head tree",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "removed", Patch: new("@@ -1 +0,0 @@\n-a")})
			}},
		// Shapes GitHub never sends, each of which the rebuild would read as
		// no change: refused.
		{name: "an entry with no filename", head: map[string]string{"main.tf": v1}, err: "an entry with no filename",
			diff: func(*testing.T) string { return `[{"status":"unchanged"}]` }},
		{name: "an entry with an empty filename", head: map[string]string{"main.tf": v1}, err: "an entry with no filename",
			diff: func(*testing.T) string { return `[{"filename":"","status":"removed"}]` }},
		{name: "an unchanged entry with a patch", head: map[string]string{"main.tf": v2}, err: "unchanged with a patch",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "unchanged", Patch: new(edit)})
			}},
		{name: "an unchanged entry the head lacks", head: map[string]string{"main.tf": v1}, err: "unchanged in the diff but not in the head tree",
			diff: func(t *testing.T) string { return ghList(t, ghFile{Filename: "gone.tf", Status: "unchanged"}) }},
		{name: "a zero-line hunk", head: map[string]string{"main.tf": v1}, err: "hunk header of no line",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -0,0 +0,0 @@")})
			}},
		{name: "a zero-line hunk in a unified diff", head: map[string]string{"main.tf": v1}, err: "hunk header of no line",
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\n--- a/main.tf\n+++ b/main.tf\n@@ -0,0 +0,0 @@\n"
			}},
		{
			name: "copied with a previous filename",
			head: map[string]string{"a.tf": v1, "b.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "copied"})
			},
			want: map[string]string{"a.tf": v1},
		},
		{name: "renamed with a hunk that cancels out", head: map[string]string{"b.tf": v1}, err: "patch changes nothing",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed", Patch: new("@@ -1 +1 @@\n-a = 1\n+a = 1")})
			}},
		{name: "a copy to itself", head: map[string]string{"main.tf": v1}, err: "copied to itself",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", PreviousFilename: "main.tf", Status: "copied"})
			}},
		{name: "a unified rename to itself", head: map[string]string{"main.tf": v1}, err: "renamed or copied to itself",
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\nsimilarity index 100%\nrename from main.tf\nrename to main.tf\n"
			}},
		{name: "a unified copy to itself", head: map[string]string{"main.tf": v1}, err: "renamed or copied to itself",
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\nsimilarity index 100%\ncopy from main.tf\ncopy to main.tf\n"
			}},
		{name: "a unified section of an index line alone", head: map[string]string{"main.tf": v1}, err: "has no hunk and no change",
			diff: func(*testing.T) string { return "diff --git a/main.tf b/main.tf\nindex 1111111..2222222 100644\n" }},
		{name: "a unified added section of side lines alone", head: map[string]string{"main.tf": v1, "x.tf": ""}, err: "has no hunk and no change",
			diff: func(*testing.T) string { return "--- /dev/null\n+++ b/x.tf\n" }},
		{name: "a unified renamed section of side lines alone", head: map[string]string{"main.tf": v1}, err: "has no hunk and no change",
			diff: func(*testing.T) string { return "--- a/old.tf\n+++ b/main.tf\n" }},
		{name: "a unified section of side lines alone", head: map[string]string{"main.tf": v1}, err: "has no hunk and no change",
			diff: func(*testing.T) string { return "--- a/main.tf\n+++ b/main.tf\n" }},
		// Header-only sections git writes (git diff -M -C --find-copies-harder
		// of a mode change, an empty file added and removed, a rename and a
		// copy): accepted.
		{
			name: "git header-only sections",
			head: map[string]string{"m.tf": "a\n", "n.tf": "", "r2.tf": "x\n", "c.tf": "c\n", "c2.tf": "c\n"},
			diff: func(*testing.T) string {
				return "diff --git a/m.tf b/m.tf\nold mode 100644\nnew mode 100755\n" +
					"diff --git a/e.tf b/e.tf\ndeleted file mode 100644\nindex e69de29..0000000\n" +
					"diff --git a/n.tf b/n.tf\nnew file mode 100644\nindex 0000000..e69de29\n" +
					"diff --git a/r.tf b/r2.tf\nsimilarity index 100%\nrename from r.tf\nrename to r2.tf\n" +
					"diff --git a/c.tf b/c2.tf\nsimilarity index 100%\ncopy from c.tf\ncopy to c2.tf\n"
			},
			want: map[string]string{"m.tf": "a\n", "e.tf": "", "r.tf": "x\n", "c.tf": "c\n"},
		},
		{name: "a context-only hunk", head: map[string]string{"main.tf": v1}, err: "hunk of context alone",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1 +1 @@\n a = 1")})
			}},
		{name: "a context-only hunk in a unified diff", head: map[string]string{"main.tf": v1}, err: "hunk of context alone",
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\n--- a/main.tf\n+++ b/main.tf\n@@ -1 +1 @@\n a = 1\n"
			}},
		{name: "a net-zero hunk", head: map[string]string{"main.tf": v1}, err: "patch changes nothing",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1 +1 @@\n-a = 1\n+a = 1"), Additions: 1, Deletions: 1})
			}},
		{name: "a net-zero hunk in a unified diff", head: map[string]string{"main.tf": v1}, err: "patch changes nothing",
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\n--- a/main.tf\n+++ b/main.tf\n@@ -1 +1 @@\n-a = 1\n+a = 1\n"
			}},
		{name: "a rename to itself", head: map[string]string{"main.tf": v1}, err: "renamed to itself",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", PreviousFilename: "main.tf", Status: "renamed", Patch: new("@@ -1 +1 @@\n-a = 0\n+a = 1")})
			}},
		{name: "a previous filename on an unchanged entry", head: map[string]string{"main.tf": v1}, err: "a previous filename on a unchanged entry",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", PreviousFilename: "evil.tf", Status: "unchanged"})
			}},
		{name: "a previous filename on a modified entry", head: map[string]string{"main.tf": v2}, err: "a previous filename on a modified entry",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", PreviousFilename: "evil.tf", Status: "modified", Patch: new(edit)})
			}},
		{name: "a zero-line hunk on an added entry", head: map[string]string{"main.tf": v1}, err: "hunk header of no line",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "added", Patch: new("@@ -0,0 +0,0 @@")})
			}},
		{name: "a context-only hunk on an added entry", head: map[string]string{"main.tf": v1}, err: "hunk of context alone",
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "main.tf", Status: "added", Patch: new("@@ -1 +1 @@\n a = 1")})
			}},
		{name: "a CRLF binary marker in a unified diff", head: map[string]string{"main.tf": v1},
			diff: func(*testing.T) string {
				return "diff --git a/main.tf b/main.tf\nindex 1111111..2222222 100644\nGIT binary patch\r\nliteral 0\r\n"
			}, want: map[string]string{"main.tf": noPatch}},
		{name: "a second JSON value", head: map[string]string{"main.tf": v1}, err: "more than one JSON value",
			diff: func(*testing.T) string { return "[] []" }},
		{name: "neither format", head: map[string]string{"main.tf": v1}, err: "neither",
			diff: func(*testing.T) string { return "hello\n" }},
		{name: "quoted git path", head: map[string]string{"main.tf": v1}, err: "unsafe path",
			diff: func(*testing.T) string { return "diff --git \"a/x\\ty\" \"b/x\\ty\"\nnew file mode 100644\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := mustTree(t, writeTree(t, tc.head))
			got, _, err := baseFromDiff(head, []byte(tc.diff(t)), DefaultLimits)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Paths, slices.Sorted(maps.Keys(tc.want))) {
				t.Fatalf("paths %v, want %v", got.Paths, slices.Sorted(maps.Keys(tc.want)))
			}
			for p, w := range tc.want {
				e := got.Entries[p]
				switch {
				case w == noPatch:
					if e.Kind != KindNoPatch || e.Data != nil {
						t.Errorf("%s: kind %s, want %s", p, e.Kind, KindNoPatch)
					}
				case strings.HasPrefix(w, "->"):
				case e.Kind != KindFile || string(e.Data) != w:
					t.Errorf("%s: kind %s, data %q, want %q", p, e.Kind, e.Data, w)
				}
			}
			if e := got.DirEntries("gone"); tc.name == "added and removed" && len(e) != 1 {
				t.Errorf("gone/ entries %v", e)
			}
		})
	}
}

// TestReadDiffBaseRefusesInput checks the file checks: a link, a missing
// file and a file over the cap all fail.
func TestReadDiffBaseRefusesInput(t *testing.T) {
	t.Parallel()
	head := mustTree(t, writeTree(t, map[string]string{"main.tf": "a\n"}))
	dir := writeTree(t, map[string]string{"real.json": "[]", "link.json": "->real.json"})
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, []byte("["+strings.Repeat(" ", maxDiffBytes)+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"link.json", "missing.json", "big.json"} {
		if _, _, err := ReadDiffBase(head, filepath.Join(dir, p), DefaultLimits); err == nil {
			t.Errorf("%s: no error", p)
		}
	}
	if _, _, err := ReadDiffBase(head, filepath.Join(dir, "real.json"), DefaultLimits); err != nil {
		t.Errorf("real.json: %v", err)
	}
}

// TestBaseFromDiffLimits checks a rebuilt file over the file size limit is
// oversize, and a base over the total limit is an error.
func TestBaseFromDiffLimits(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{"main.tf": "a\n"}))
	diff := ghList(t, ghFile{Filename: "main.tf", Status: "modified", Patch: new("@@ -1,2 +1 @@\n a\n-" + strings.Repeat("x", 100))})
	lim := DefaultLimits
	lim.MaxFileBytes = 50
	got, _, err := baseFromDiff(head, []byte(diff), lim)
	if err != nil || got.Entries["main.tf"].Kind != KindOversize {
		t.Fatalf("err %v, entry %+v", err, got.Entries["main.tf"])
	}
	lim = DefaultLimits
	lim.MaxTotalBytes = 50
	if _, _, err := baseFromDiff(head, []byte(diff), lim); err == nil || !strings.Contains(err.Error(), "more than 50 bytes") {
		t.Errorf("total limit: %v", err)
	}
}

// TestBaseFromDiffQuotesHunkPath: the path of a unified diff is read before
// the path check runs, so the error for a bad hunk quotes it: a carriage
// return in it cannot rewrite a line of stderr. Every later error names a
// path safeDiffPath has accepted, and quotes it as well.
func TestBaseFromDiffQuotesHunkPath(t *testing.T) {
	t.Parallel()
	head := mustTree(t, writeTree(t, map[string]string{"main.tf": "a\n"}))
	diff := "diff --git a/x\ry.tf b/x\ry.tf\n--- a/x\ry.tf\n+++ b/x\ry.tf\n@@ garbage\n"
	_, _, err := baseFromDiff(head, []byte(diff), DefaultLimits)
	if err == nil || !strings.Contains(err.Error(), `unified diff: "x\ry.tf": `) {
		t.Fatalf("error %v, want the quoted path", err)
	}
	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("error holds a raw control character: %q", err)
	}
}
