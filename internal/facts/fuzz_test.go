package facts

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzSeeds adds every file under dir whose name ends in one of suffixes as
// a seed, up to limit files. The walk reads through an os.Root, so no link
// in testdata leads outside dir.
func fuzzSeeds(f *testing.F, dir string, limit int, suffixes ...string) {
	f.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		f.Fatal(err)
	}
	defer root.Close()
	fsys := root.FS()
	n := 0
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || n >= limit {
			return err
		}
		for _, s := range suffixes {
			if strings.HasSuffix(p, s) {
				data, err := fs.ReadFile(fsys, p)
				if err != nil {
					return err
				}
				f.Add(data)
				n++
				break
			}
		}
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}
	if n == 0 {
		f.Fatalf("no seeds under %s", dir)
	}
}

// limitSeeds are files at the nesting depth and unary operator limits of
// DefaultLimits and one over each, so the fuzzer starts at the guards.
func limitSeeds(f *testing.F) {
	f.Helper()
	for _, n := range []int{DefaultLimits.MaxDepth, DefaultLimits.MaxDepth + 1} {
		f.Add([]byte("a = " + strings.Repeat("[", n) + strings.Repeat("]", n) + "\n"))
		f.Add([]byte("a = " + strings.Repeat("(", n) + "1" + strings.Repeat(")", n) + "\n"))
	}
	for _, n := range []int{DefaultLimits.MaxPrefixOps, DefaultLimits.MaxPrefixOps + 1} {
		f.Add([]byte("a = " + strings.Repeat("!", n) + "true\n"))
		f.Add([]byte("a = " + strings.Repeat("-", n) + "1\n"))
	}
}

// FuzzParseSyntax feeds arbitrary bytes to the HCL parse every reader of a
// tree goes through. A parse error or a limit is fine. A panic is not, and
// neither is the recovered one parseSyntax reports as an internal error: it
// is a parser crash the recover only hides. An accepted file stays within
// the token, depth and unary operator limits.
func FuzzParseSyntax(f *testing.F) {
	fuzzSeeds(f, "../../testdata", 200, ".tf", ".tftest.hcl")
	limitSeeds(f)
	lim := DefaultLimits
	f.Fuzz(func(t *testing.T, src []byte) {
		body, tokens, err := parseSyntax("main.tf", src, lim)
		if err != nil {
			if body != nil || tokens != nil {
				t.Fatalf("error %v with a body or tokens", err)
			}
			if err.Error() == "internal error while parsing" {
				t.Fatalf("the parser panicked on %q", src)
			}
			return
		}
		if body == nil {
			t.Fatal("no error and no body")
		}
		if len(tokens) > lim.MaxTokens || nestingDepth(tokens) > lim.MaxDepth || prefixRun(tokens) > lim.MaxPrefixOps {
			t.Fatalf("accepted a file over a limit: %d tokens", len(tokens))
		}
	})
}

// FuzzParseFile feeds arbitrary bytes to parseFile, which parses a file and
// walks it into a module's facts. A parse error or a limit is fine; a panic,
// or one parseFile recovers as an internal error, is not.
func FuzzParseFile(f *testing.F) {
	fuzzSeeds(f, "../../testdata", 200, ".tf")
	limitSeeds(f)
	f.Fuzz(func(t *testing.T, src []byte) {
		m := &Module{Dir: ".", varNames: map[string]bool{}, outNames: map[string]bool{}}
		if err := parseFile(m, "main.tf", src, DefaultLimits); err != nil && err.Error() == "internal error while parsing" {
			t.Fatalf("parsing or collecting panicked on %q", src)
		}
	})
}

// fuzzDiffBase rebuilds a base tree from data against the diff-base head
// tree. An error leaves no partial tree. A rebuilt tree holds at most
// MaxFiles entries, sorted paths that each have an entry, and no file over
// MaxFileBytes.
func fuzzDiffBase(t *testing.T, head *Tree, data []byte, lim Limits) {
	base, _, err := baseFromDiff(head, data, lim)
	if err != nil {
		if base != nil {
			t.Fatalf("error %v with a partial tree", err)
		}
		return
	}
	if base == nil {
		t.Fatal("no error and no tree")
	}
	if len(base.Paths) > lim.MaxFiles || len(base.Entries) != len(base.Paths) {
		t.Fatalf("%d paths, %d entries", len(base.Paths), len(base.Entries))
	}
	for i, p := range base.Paths {
		if i > 0 && base.Paths[i-1] >= p {
			t.Fatalf("paths not sorted and unique at %q", p)
		}
		e := base.Entries[p]
		if e == nil {
			t.Fatalf("%q has no entry", p)
		}
		if int64(len(e.Data)) > lim.MaxFileBytes {
			t.Fatalf("%q holds %d bytes", p, len(e.Data))
		}
	}
}

func fuzzHead(f *testing.F) *Tree {
	f.Helper()
	head, err := ReadTree(filepath.Join(diffFixture, "head"), DefaultLimits)
	if err != nil {
		f.Fatal(err)
	}
	return head
}

// FuzzDiffBaseGitHubFiles fuzzes ReadDiffBase's rebuild from a GitHub pull
// request files list.
func FuzzDiffBaseGitHubFiles(f *testing.F) {
	data, err := os.ReadFile(filepath.Join(diffFixture, "files.json"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"filename":"main.tf","status":"renamed","previous_filename":"old.tf"}]`))
	head := fuzzHead(f)
	lim := DefaultLimits
	lim.MaxFiles = 64
	f.Fuzz(func(t *testing.T, data []byte) {
		if s := bytes.TrimSpace(data); len(s) == 0 || s[0] != '[' {
			return // the unified diff target covers the rest
		}
		fuzzDiffBase(t, head, data, lim)
	})
}

// FuzzDiffBaseUnified fuzzes ReadDiffBase's rebuild from a unified diff.
func FuzzDiffBaseUnified(f *testing.F) {
	data, err := os.ReadFile(filepath.Join(diffFixture, "change.diff"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte("diff --git a/main.tf b/main.tf\ndeleted file mode 100644\n"))
	head := fuzzHead(f)
	lim := DefaultLimits
	lim.MaxFiles = 64
	f.Fuzz(func(t *testing.T, data []byte) {
		if s := bytes.TrimSpace(data); len(s) > 0 && s[0] == '[' {
			return // the files list target covers these
		}
		fuzzDiffBase(t, head, data, lim)
	})
}

// FuzzReadYAML fuzzes the YAML subset reader of the scope rules and the
// pre-commit reader on top of it. A refusal returns nothing. An accepted
// file gives at most one line per input line, in order, and every index
// listItems and fields return is a line of the file.
func FuzzReadYAML(f *testing.F) {
	fuzzSeeds(f, "../../testdata", 50, ".yaml", ".yml")
	f.Add([]byte("repos:\n  - repo: a\n    hooks:\n      - id: b\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ls, ok := readYAML(data)
		if !ok {
			if ls != nil {
				t.Fatal("refused with lines")
			}
			return
		}
		lines := len(splitLines(data))
		if len(ls) > lines {
			t.Fatalf("%d lines read from %d", len(ls), lines)
		}
		for i, l := range ls {
			if l.n < 1 || l.n > lines || (i > 0 && ls[i-1].n >= l.n) {
				t.Fatalf("line %d numbered %d", i, l.n)
			}
		}
		for i := range ls {
			if items, ok := listItems(ls, i); ok {
				for _, j := range items {
					if j < 0 || j >= len(ls) {
						t.Fatalf("list item %d of %d lines", j, len(ls))
					}
				}
			}
			if fs, ok := fields(ls, i); ok {
				for _, j := range fs {
					if j < 0 || j >= len(ls) {
						t.Fatalf("field %d of %d lines", j, len(ls))
					}
				}
			}
		}
		if pairs, ok := readPreCommit(data); ok {
			for _, p := range pairs {
				if p.repoLine < 1 || p.repoLine > lines || p.idLine < 1 || p.idLine > lines {
					t.Fatalf("pair %+v outside %d lines", p, lines)
				}
			}
		}
	})
}
