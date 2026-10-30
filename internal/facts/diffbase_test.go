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
			want: map[string]string{"a.tf": v1},
		},
		{
			name: "pure rename of a text file",
			head: map[string]string{"b.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: "b.tf", PreviousFilename: "a.tf", Status: "renamed"})
			},
			want: map[string]string{"a.tf": v1},
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
			name: "paths under .git and .terraform are skipped",
			head: map[string]string{"main.tf": v1},
			diff: func(t *testing.T) string {
				return ghList(t, ghFile{Filename: ".terraform/x.tf", Status: "added"}, ghFile{Filename: "a/.git/config", Status: "removed"})
			},
			want: map[string]string{"main.tf": v1},
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
