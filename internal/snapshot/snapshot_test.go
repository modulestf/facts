package snapshot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/profile"
)

const testProfile = "../../testdata/profile"

// copyProfile copies the test profile to a temp dir a test may change.
func copyProfile(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "profile")
	if err := os.CopyFS(dst, os.DirFS(testProfile)); err != nil {
		t.Fatal(err)
	}
	return dst
}

func take(t *testing.T, src string) *Snapshot {
	t.Helper()
	s, err := Take(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(s.Dir) })
	return s
}

func TestTakeCopiesAndHashes(t *testing.T) {
	src := copyProfile(t)
	if err := os.WriteFile(filepath.Join(src, "review-guide.md"), []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := take(t, src)
	st, err := os.Lstat(s.Dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("snapshot dir %v, mode %v", err, st.Mode())
	}
	files := readAll(t, src)
	if got := readAll(t, s.Dir); len(got) != len(files) || string(got["review-guide.md"]) != "guide\n" {
		t.Errorf("snapshot holds %d files, want %d", len(got), len(files))
	}
	if s.TreeSHA256 != profile.TreeSHA256(files) {
		t.Errorf("tree_sha256 %s, want %s", s.TreeSHA256, profile.TreeSHA256(files))
	}
	p, err := profile.New(files)
	if err != nil {
		t.Fatal(err)
	}
	if s.ProfileSHA256 == nil || *s.ProfileSHA256 != p.SHA256 {
		t.Errorf("profile_sha256 %v, want %s", s.ProfileSHA256, p.SHA256)
	}
	if again := take(t, s.Dir); again.TreeSHA256 != s.TreeSHA256 || *again.ProfileSHA256 != *s.ProfileSHA256 {
		t.Error("a snapshot of the snapshot hashes differently")
	}
}

func TestTakeInvalidProfileHasNoProfileHash(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "PROFILE.md"), []byte("no blocks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := take(t, src); s.ProfileSHA256 != nil || s.TreeSHA256 == "" {
		t.Errorf("profile_sha256 %v, tree %q", s.ProfileSHA256, s.TreeSHA256)
	}
}

func TestTakeRefuses(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, dir string){
		"symlink file": func(t *testing.T, dir string) { must(t, os.Symlink("PROFILE.md", filepath.Join(dir, "link.md"))) },
		"symlink dir":  func(t *testing.T, dir string) { must(t, os.Symlink("templates", filepath.Join(dir, "tpl"))) },
		"dangling":     func(t *testing.T, dir string) { must(t, os.Symlink("/nonexistent", filepath.Join(dir, "x"))) },
		"fifo":         func(t *testing.T, dir string) { must(t, syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644)) },
		"too many files": func(t *testing.T, dir string) {
			for i := range maxEntries {
				must(t, os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), nil, 0o644))
			}
		},
		"too many bytes": func(t *testing.T, dir string) {
			must(t, os.WriteFile(filepath.Join(dir, "big"), make([]byte, maxBytes+1), 0o644))
		},
		"too deep": func(t *testing.T, dir string) {
			deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "g", "h", "i")
			must(t, os.MkdirAll(deep, 0o755))
		},
		"other device": func(t *testing.T, _ string) {
			orig := devOf
			devOf = func(fi os.FileInfo) uint64 {
				if fi.Name() == "PROFILE.md" {
					return orig(fi) + 1
				}
				return orig(fi)
			}
			t.Cleanup(func() { devOf = orig })
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := copyProfile(t)
			setup(t, src)
			done := make(chan error, 1)
			go func() {
				s, err := Take(src)
				if err == nil {
					_ = os.RemoveAll(s.Dir)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrRefused) {
					t.Errorf("error %v, want ErrRefused", err)
				}
				if err != nil && strings.Contains(err.Error(), src) {
					t.Errorf("error names the host path: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Take hangs")
			}
		})
	}
}

func TestTakeDepthLimitEdge(t *testing.T) {
	src := copyProfile(t)
	must(t, os.MkdirAll(filepath.Join(src, "a", "b", "c", "d", "e", "f", "g"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "d", "e", "f", "g", "x"), []byte("x"), 0o644))
	take(t, src)
}

func TestTakeRefusesSourceLink(t *testing.T) {
	src := copyProfile(t)
	link := filepath.Join(t.TempDir(), "link")
	must(t, os.Symlink(src, link))
	if _, err := Take(link); err == nil {
		t.Error("a linked source directory is accepted")
	}
	if _, err := Take(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing source directory is accepted")
	}
}

func readAll(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	fsys := os.DirFS(dir)
	must(t, fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out[p], err = fs.ReadFile(fsys, p)
		return err
	}))
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
