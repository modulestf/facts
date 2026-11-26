//go:build unix

package stats

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inTree is inTreeAt on a descriptor of the parent directory of p.
func (g Guard) inTree(p string) error {
	dfd, _, err := openParent(p)
	if err != nil {
		return err
	}
	defer closeFd(dfd)
	return g.inTreeAt(p, dfd)
}

// TestWriteFileOtherOwner pretends to be another user, as a file planted
// by someone else would look, and checks that the file is left alone.
func TestWriteFileOtherOwner(t *testing.T) {
	p := filepath.Join(t.TempDir(), "theirs.json")
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	own := euid
	euid = func() int { return own() + 1 }
	t.Cleanup(func() { euid = own })
	if err := WriteFile(p, []byte("{}\n"), Guard{}); err == nil {
		t.Error("a file of another owner was written")
	}
	if got, _ := os.ReadFile(p); string(got) != "keep" {
		t.Errorf("file changed to %q", got)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	data := []byte("{}\n")

	t.Run("new", func(t *testing.T) {
		p := filepath.Join(dir, "new.json")
		if err := WriteFile(p, data, Guard{}); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", st.Mode().Perm())
		}
	})
	t.Run("own regular file is replaced", func(t *testing.T) {
		p := filepath.Join(dir, "own.json")
		if err := os.WriteFile(p, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(p, data, Guard{}); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(p); string(got) != string(data) {
			t.Errorf("content %q, want %q", got, data)
		}
	})
	t.Run("link refused and target unchanged", func(t *testing.T) {
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "link.json")
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(p, data, Guard{}); err == nil {
			t.Error("a link was written through")
		}
		if got, _ := os.ReadFile(target); string(got) != "keep" {
			t.Errorf("target changed to %q", got)
		}
	})
	t.Run("dangling link refused and nothing created", func(t *testing.T) {
		target := filepath.Join(dir, "absent")
		p := filepath.Join(dir, "dangling.json")
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(p, data, Guard{}); err == nil {
			t.Error("a dangling link was written through")
		}
		if _, err := os.Lstat(target); err == nil {
			t.Error("the link target was created")
		}
	})
	t.Run("hard link refused and unchanged", func(t *testing.T) {
		a := filepath.Join(dir, "hard-a")
		if err := os.WriteFile(a, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		b := filepath.Join(dir, "hard-b")
		if err := os.Link(a, b); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(b, data, Guard{}); err == nil {
			t.Error("a hard-linked file was written")
		}
		if got, _ := os.ReadFile(a); string(got) != "keep" {
			t.Errorf("hard link changed to %q", got)
		}
	})
	t.Run("directory refused", func(t *testing.T) {
		if err := WriteFile(dir, data, Guard{}); err == nil {
			t.Error("a directory was accepted")
		}
	})
	t.Run("fifo refused without blocking", func(t *testing.T) {
		p := filepath.Join(dir, "fifo")
		if err := mkfifo(p); err != nil {
			t.Skip("no fifo:", err)
		}
		done := make(chan error, 1)
		go func() { done <- WriteFile(p, data, Guard{}) }()
		select {
		case err := <-done:
			if err == nil {
				t.Error("a fifo was accepted")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("WriteFile blocked on a fifo")
		}
	})
	t.Run("same file as an output refused", func(t *testing.T) {
		p := filepath.Join(dir, "stdout.txt")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString("report"); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(p, data, Guard{Outputs: []*os.File{f}}); err == nil {
			t.Error("the file stdout goes to was overwritten")
		}
		if got, _ := os.ReadFile(p); string(got) != "report" {
			t.Errorf("output file changed to %q", got)
		}
	})
	t.Run("over the size cap refused", func(t *testing.T) {
		p := filepath.Join(dir, "big.json")
		if err := WriteFile(p, make([]byte, MaxRecordBytes+1), Guard{}); err == nil {
			t.Error("a record over the cap was written")
		}
		if _, err := os.Lstat(p); err == nil {
			t.Error("a file was created for a record over the cap")
		}
	})
	t.Run("missing directory", func(t *testing.T) {
		if err := WriteFile(filepath.Join(dir, "no", "such.json"), data, Guard{}); err == nil {
			t.Error("a path in a missing directory was accepted")
		}
	})
}

// TestWriteFileSwapped swaps a checked regular file for a link, a
// directory or a fifo between the Lstat and the open: the checks on the
// file opened must refuse each, without blocking and without writing.
func TestWriteFileSwapped(t *testing.T) {
	for _, kind := range []string{"link", "dir", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, "stats.json")
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			beforeOpen = func() {
				if err := os.Remove(p); err != nil {
					t.Error(err)
				}
				var err error
				switch kind {
				case "link":
					err = os.Symlink(target, p)
				case "dir":
					err = os.Mkdir(p, 0o700)
				case "fifo":
					err = mkfifo(p)
				}
				if err != nil {
					t.Error(err)
				}
			}
			t.Cleanup(func() { beforeOpen = nil })
			done := make(chan error, 1)
			go func() { done <- WriteFile(p, []byte("{}\n"), Guard{}) }()
			select {
			case err := <-done:
				if err == nil {
					t.Errorf("a %s swapped in was written", kind)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("WriteFile blocked on a %s swapped in", kind)
			}
			if got, _ := os.ReadFile(target); string(got) != "keep" {
				t.Errorf("link target changed to %q", got)
			}
		})
	}
}

// TestWriteFileInputs refuses a path that is an input file of the run, or
// whose directory is an input tree or lies in one, through a link too.
func TestWriteFileInputs(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "modules", "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "files.json")
	for _, p := range []string{input, filepath.Join(tree, "main.tf"), filepath.Join(tree, "modules", "a", "main.tf")} {
		if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(tree, filepath.Join(dir, "tree-link")); err != nil {
		t.Fatal(err)
	}
	g := Guard{Files: []string{input}, Trees: []string{tree}}
	for _, p := range []string{
		input,
		filepath.Join(tree, "main.tf"),
		filepath.Join(tree, "new.json"),
		filepath.Join(tree, "modules", "a", "main.tf"),
		filepath.Join(dir, "tree-link", "main.tf"),
	} {
		if err := WriteFile(p, []byte("{}\n"), g); err == nil {
			t.Errorf("%s: written", p)
		}
	}
	for _, p := range []string{input, filepath.Join(tree, "main.tf"), filepath.Join(tree, "modules", "a", "main.tf")} {
		if got, _ := os.ReadFile(p); string(got) != "keep" {
			t.Errorf("%s changed to %q", p, got)
		}
	}
	if _, err := os.Lstat(filepath.Join(tree, "new.json")); err == nil {
		t.Error("a file was created in the input tree")
	}
	// A linked tree root resolves the same way, and a sibling of the tree
	// whose name starts with it is outside.
	if err := WriteFile(filepath.Join(tree, "x.json"), []byte("{}\n"), Guard{Trees: []string{filepath.Join(dir, "tree-link")}}); err == nil {
		t.Error("a path in a tree named through a link was written")
	}
	sibling := filepath.Join(dir, "tree2")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(sibling, "stats.json"), []byte("{}\n"), g); err != nil {
		t.Errorf("a sibling of the tree was refused: %v", err)
	}
}

// TestWriteFileDevice pretends to be root, which owns /dev/null: the
// device must still be refused as no regular file, by the checks before or
// after the open, never by a later truncate or write error.
func TestWriteFileDevice(t *testing.T) {
	own := euid
	euid = func() int { return 0 }
	t.Cleanup(func() { euid = own })
	if err := WriteFile(os.DevNull, []byte("{}\n"), Guard{}); !errors.Is(err, errNotOwned) {
		t.Errorf("err %v, want the refusal of a file that is no regular file", err)
	}
}

// TestWriteFileTreeAlias names the input tree through another spelling of
// the same directory, in a lower-cased name on a case-insensitive file
// system and through a link into a directory of the tree: both are the
// tree and are refused.
func TestWriteFileTreeAlias(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "Tree")
	if err := os.MkdirAll(filepath.Join(tree, "modules", "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tree, "main.tf")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := Guard{Trees: []string{tree}}
	t.Run("link into a subdirectory", func(t *testing.T) {
		link := filepath.Join(dir, "link")
		if err := os.Symlink(filepath.Join(tree, "modules"), link); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(link, "new.json")
		if err := WriteFile(p, []byte("{}\n"), g); err == nil {
			t.Error("a path in the tree reached through a link was written")
		}
		if _, err := os.Lstat(filepath.Join(tree, "modules", "new.json")); err == nil {
			t.Error("a file was created in the input tree")
		}
	})
	t.Run("link then dotdot", func(t *testing.T) {
		// lnk/.. is the physical parent of what lnk points to, the tree,
		// while the text lnk/.. names dir.
		lnk := filepath.Join(dir, "lnk")
		if err := os.Symlink(filepath.Join(tree, "modules"), lnk); err != nil {
			t.Fatal(err)
		}
		p := lnk + "/../main.tf"
		if err := WriteFile(p, []byte("{}\n"), g); err == nil {
			t.Error("lnk/../main.tf was written")
		}
		if err := g.inTree(p); err == nil {
			t.Error("the walk did not find the tree through lnk/..")
		}
		if got, _ := os.ReadFile(target); string(got) != "keep" {
			t.Errorf("%s changed to %q", target, got)
		}
	})
	t.Run("plain dotdot", func(t *testing.T) {
		p := tree + "/../outside.json"
		if err := WriteFile(p, []byte("{}\n"), g); err == nil {
			t.Error("a path with a .. element was written")
		}
		if _, err := os.Lstat(filepath.Join(dir, "outside.json")); err == nil {
			t.Error("a file was created through a .. path")
		}
	})
	t.Run("link chain through the tree", func(t *testing.T) {
		deep := filepath.Join(dir, "deep")
		if err := os.Symlink(filepath.Join(tree, "modules", "a"), deep); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(deep, "new.json")
		if err := WriteFile(p, []byte("{}\n"), g); err == nil {
			t.Error("a path whose physical parents pass through the tree was written")
		}
		if err := g.inTree(p); err == nil {
			t.Error("the walk did not find the tree above the link's target")
		}
	})
	t.Run("case alias", func(t *testing.T) {
		alias := filepath.Join(dir, "tree")
		if _, err := os.Stat(alias); err != nil {
			t.Skip("the temp file system is case-sensitive")
		}
		if err := WriteFile(filepath.Join(alias, "main.tf"), []byte("{}\n"), g); err == nil {
			t.Error("the tree named in another case was written")
		}
		if got, _ := os.ReadFile(target); string(got) != "keep" {
			t.Errorf("%s changed to %q", target, got)
		}
	})
}

// TestWriteFileLongPath writes a record at a valid path of about 1000 bytes
// outside every tree: the walk must not grow the path past the system's
// limit.
func TestWriteFileLongPath(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "out")
	for len(deep) < 950 {
		deep = filepath.Join(deep, strings.Repeat("d", 40))
	}
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(deep, "stats.json")
	if err := WriteFile(p, []byte("{}\n"), Guard{Trees: []string{tree}}); err != nil {
		t.Fatalf("a %d-byte path outside every tree was refused: %v", len(p), err)
	}
	if got, _ := os.ReadFile(p); string(got) != "{}\n" {
		t.Errorf("content %q", got)
	}
}

// TestWriteFileUncheckable runs from a working directory whose parent
// cannot be searched: the file could be created relative to it, but the
// walk cannot stat the directories above, so the path is refused and
// nothing is created.
func TestWriteFileUncheckable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root searches any directory")
	}
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	locked := filepath.Join(dir, "locked")
	work := filepath.Join(locked, "work")
	for _, d := range []string{tree, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)
	t.Setenv("PWD", work)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	err := WriteFile("stats.json", []byte("{}\n"), Guard{Trees: []string{tree}})
	if err == nil || !strings.Contains(err.Error(), "cannot check it against the input trees") {
		t.Errorf("err %v, want the refusal of a path whose directories cannot be checked", err)
	}
	if err := os.Chmod(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(work, "stats.json")); err == nil {
		t.Error("a file was created")
	}
}

// TestWriteFileParentRace swaps the parent directory of the path between a
// directory outside the trees and one inside, in a loop, while records are
// written: no record may ever land in the tree.
func TestWriteFileParentRace(t *testing.T) {
	if testing.Short() {
		t.Skip("races for a few seconds")
	}
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	out := filepath.Join(dir, "out")
	for _, d := range []string{tree, out} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "p")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := filepath.Join(dir, "p.tmp")
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			target := out
			if i%2 == 1 {
				target = tree
			}
			_ = os.Remove(tmp)
			if err := os.Symlink(target, tmp); err == nil {
				_ = os.Rename(tmp, link)
			}
		}
	}()
	g := Guard{Trees: []string{tree}}
	landed := filepath.Join(tree, "stats.json")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = WriteFile(filepath.Join(link, "stats.json"), []byte("{}\n"), g)
		if _, err := os.Lstat(landed); err == nil {
			break
		}
	}
	close(stop)
	<-done
	if _, err := os.Lstat(landed); err == nil {
		t.Error("a record landed in the input tree")
	}
}
