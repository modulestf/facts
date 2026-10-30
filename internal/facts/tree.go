//go:build unix

package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Limits bound what one run reads and writes.
type Limits struct {
	MaxFiles       int   // directory entries in one tree, of every kind
	MaxFileBytes   int64 // a larger file is recorded as oversize and not read
	MaxTotalBytes  int64 // bytes read from one tree
	MaxDepth       int   // HCL bracket nesting depth per file
	MaxOutputBytes int   // size of the JSON report
	MaxTokens      int   // HCL tokens per file
	MaxPrefixOps   int   // consecutive unary operators, which the parser recurses on
	// Deadline stops a run that takes too long. Zero means no deadline.
	Deadline time.Time
}

// ErrDeadline is returned when a run passes Limits.Deadline.
var ErrDeadline = errors.New("deadline exceeded")

func (l Limits) expired() error {
	if !l.Deadline.IsZero() && time.Now().After(l.Deadline) {
		return ErrDeadline
	}
	return nil
}

// DefaultLimits are the limits the CLI uses.
var DefaultLimits = Limits{
	MaxFiles:       10000,
	MaxFileBytes:   1 << 20,
	MaxTotalBytes:  64 << 20,
	MaxDepth:       64,
	MaxOutputBytes: 16 << 20,
	MaxTokens:      100000,
	MaxPrefixOps:   64,
}

// Entry kinds.
const (
	KindFile       = "file"
	KindSymlink    = "symlink"
	KindSpecial    = "special"
	KindUnreadable = "unreadable"
	KindOversize   = "oversize"
	// KindNoPatch is a base file rebuilt from a diff that has no patch for
	// it, such as a binary or very large file: its base content is unknown.
	KindNoPatch = "no_patch"
)

// Entry is one non-directory path in a tree.
type Entry struct {
	Path   string
	Kind   string
	SHA256 string // set for KindFile only
	Data   []byte // set for KindFile only
	// linkSHA256 hashes a link's target text so a retarget is a change. The
	// text itself never leaves the reader.
	linkSHA256 string
	// exec is set for a regular file with any execute bit. sameEntry
	// compares it; TreeSHA256 does not hash it, so a tree hash a report or a
	// schema handoff recorded stays what it was.
	exec bool
}

// Executable reports whether e is a regular file with an execute bit.
func (e *Entry) Executable() bool { return e.exec }

// Same reports whether e and o are unchanged between trees, mode included.
func (e *Entry) Same(o *Entry) bool { return sameEntry(e, o) }

// Tree is a confined read of one directory tree.
type Tree struct {
	Entries map[string]*Entry
	Paths   []string        // sorted entry paths
	Dirs    map[string]bool // directories, "." included
	byDir   map[string][]*Entry
}

// skipDir names directories that are never read: VCS metadata and local
// Terraform working state, neither of which is module source.
func skipDir(name string) bool { return name == ".git" || name == ".terraform" }

// readDirBatch is how many directory entries one ReadDir call returns at most.
const readDirBatch = 256

// readDirHook, when set, sees every ReadDir batch: the directory and the
// number of entries returned. Tests use it; it is nil in production.
var readDirHook func(dir string, n int)

// ReadTree reads every regular file under root. It never follows a symbolic
// link: links and special files are recorded by kind and not opened.
//
// Limits.MaxFiles is a budget for the whole tree: every directory entry
// counts, whatever its kind, and the read stops as soon as a ReadDir batch
// passes it, before reading on or opening any child.
func ReadTree(root string, lim Limits) (*Tree, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	w := &treeReader{root: r, lim: lim, t: &Tree{Entries: map[string]*Entry{}, Dirs: map[string]bool{".": true}}}
	if err := w.walk("."); err != nil {
		return nil, err
	}
	w.t.index()
	return w.t, nil
}

// index sorts Paths and groups the entries by directory.
func (t *Tree) index() {
	sort.Strings(t.Paths)
	t.byDir = map[string][]*Entry{}
	for _, p := range t.Paths {
		d := path.Dir(p)
		t.byDir[d] = append(t.byDir[d], t.Entries[p])
	}
}

// treeReader holds the state of one depth-first ReadTree walk.
type treeReader struct {
	root  *os.Root
	lim   Limits
	t     *Tree
	count int   // directory entries seen, the MaxFiles budget
	total int64 // bytes read, the MaxTotalBytes budget
}

// walk reads dir and everything below it. Only the root failing to list is
// an error; a subdirectory that cannot be listed is recorded as unreadable.
func (w *treeReader) walk(dir string) error {
	ents, readErr, err := w.list(dir)
	if err != nil {
		return err
	}
	if readErr != nil {
		if dir == "." {
			return readErr
		}
		w.t.add(&Entry{Path: dir, Kind: KindUnreadable})
		return nil
	}
	for _, d := range ents {
		if err := w.lim.expired(); err != nil {
			return err
		}
		p := path.Join(dir, d.Name())
		if d.IsDir() {
			if skipDir(d.Name()) {
				continue
			}
			w.t.Dirs[p] = true
			if err := w.walk(p); err != nil {
				return err
			}
			continue
		}
		if err := w.addEntry(p, d.Type()); err != nil {
			return err
		}
	}
	return nil
}

// list returns the entries of dir sorted by name. readErr reports a dir that
// cannot be opened or read; err reports the MaxFiles budget passed.
func (w *treeReader) list(dir string) (ents []fs.DirEntry, readErr, err error) {
	f, err := openDir(w.root, dir)
	if err != nil {
		return nil, err, nil
	}
	defer f.Close()
	for {
		batch, rerr := f.ReadDir(readDirBatch)
		if readDirHook != nil {
			readDirHook(dir, len(batch))
		}
		w.count += len(batch)
		if w.count > w.lim.MaxFiles {
			return nil, nil, fmt.Errorf("more than %d entries", w.lim.MaxFiles)
		}
		ents = append(ents, batch...)
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, rerr, nil
		}
	}
	slices.SortFunc(ents, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return ents, nil, nil
}

// openDir opens dir for listing. It fails, without blocking, when dir is or
// was swapped for a link, a fifo or any other non-directory.
func openDir(r *os.Root, dir string) (*os.File, error) {
	before, err := r.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}
	f, _, err := openUnchanged(r, dir, before, syscall.O_DIRECTORY)
	return f, err
}

// beforeOpen runs between the caller's Lstat and the open in openUnchanged.
// It does nothing; a test replaces it to swap the path at that point.
var beforeOpen = func(string) {}

// openUnchanged opens name read-only and checks it is still the file before,
// which the caller took with Lstat. os.Root follows a link inside the root
// even with O_NOFOLLOW, so this check is what refuses a path swapped for a
// link before the open. O_NONBLOCK keeps a swapped-in fifo from blocking.
func openUnchanged(r *os.Root, name string, before fs.FileInfo, flag int) (*os.File, fs.FileInfo, error) {
	beforeOpen(name)
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|flag, 0)
	if err != nil {
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		_ = f.Close() // read-only; the open is being refused
		return nil, nil, fmt.Errorf("%s: changed while it was opened", name)
	}
	return f, after, nil
}

// addEntry records one non-directory entry by kind, reading regular files.
func (w *treeReader) addEntry(p string, typ fs.FileMode) error {
	e := &Entry{Path: p}
	switch {
	case typ&fs.ModeSymlink != 0:
		e.Kind = KindSymlink
		if target, err := w.root.Readlink(p); err == nil {
			sum := sha256.Sum256([]byte(target))
			e.linkSHA256 = hex.EncodeToString(sum[:])
		}
	case !typ.IsRegular():
		e.Kind = KindSpecial
	default:
		readFile(w.root, e, w.lim.MaxFileBytes)
	}
	if e.Kind == KindFile {
		w.total += int64(len(e.Data))
		if w.total > w.lim.MaxTotalBytes {
			return fmt.Errorf("more than %d bytes", w.lim.MaxTotalBytes)
		}
	}
	w.t.add(e)
	return nil
}

func (t *Tree) add(e *Entry) {
	t.Entries[e.Path] = e
	t.Paths = append(t.Paths, e.Path)
}

// readFile reads a regular file listed by the walk. A path that is now a link
// is recorded as unreadable and a fifo or other special file as special, both
// without being read; a path swapped between the Lstat and the open is
// refused by openUnchanged and recorded as unreadable.
func readFile(r *os.Root, e *Entry, limit int64) {
	before, err := r.Lstat(e.Path)
	switch {
	case err != nil, before.Mode()&fs.ModeSymlink != 0:
		e.Kind = KindUnreadable // gone, or swapped for a link after the listing
		return
	case !before.Mode().IsRegular():
		e.Kind = KindSpecial
		return
	}
	e.exec = before.Mode().Perm()&0o111 != 0
	f, st, err := openUnchanged(r, e.Path, before, 0)
	if err != nil {
		e.Kind = KindUnreadable
		return
	}
	defer f.Close()
	if st.Size() > limit {
		e.Kind = KindOversize
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		e.Kind = KindUnreadable
		return
	}
	if int64(len(data)) > limit {
		e.Kind = KindOversize
		return
	}
	sum := sha256.Sum256(data)
	e.Kind, e.Data, e.SHA256 = KindFile, data, hex.EncodeToString(sum[:])
}

// DirEntries returns the entries directly in dir, sorted by path.
func (t *Tree) DirEntries(dir string) []*Entry { return t.byDir[dir] }

// TreeSHA256 hashes the sorted list of paths, kinds and file hashes.
func (t *Tree) TreeSHA256() string {
	h := sha256.New()
	for _, p := range t.Paths {
		e := t.Entries[p]
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\n", e.Path, e.Kind, e.SHA256, e.linkSHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sameEntry reports whether two entries are unchanged between trees.
func sameEntry(a, b *Entry) bool {
	if a == nil || b == nil || a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindFile:
		return a.SHA256 == b.SHA256 && a.exec == b.exec
	case KindSymlink:
		return a.linkSHA256 != "" && a.linkSHA256 == b.linkSHA256
	case KindSpecial:
		return true
	}
	return false // unreadable or oversize: unknown, so treated as changed
}
