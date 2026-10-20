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
	"sort"
	"syscall"
	"time"
)

// Limits bound what one run reads and writes.
type Limits struct {
	MaxFiles       int   // entries in one tree, files and links and special files
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
}

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

// ReadTree reads every regular file under root. It never follows a symbolic
// link: links and special files are recorded by kind and not opened.
func ReadTree(root string, lim Limits) (*Tree, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	t := &Tree{Entries: map[string]*Entry{}, Dirs: map[string]bool{".": true}}
	var total int64
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if p == "." {
				return werr
			}
			t.add(&Entry{Path: p, Kind: KindUnreadable})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == "." {
			return nil
		}
		if len(t.Entries)+len(t.Dirs) > lim.MaxFiles {
			return fmt.Errorf("more than %d entries", lim.MaxFiles)
		}
		if err := lim.expired(); err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			t.Dirs[p] = true
			return nil
		}
		e := &Entry{Path: p}
		switch typ := d.Type(); {
		case typ&fs.ModeSymlink != 0:
			e.Kind = KindSymlink
			if target, err := r.Readlink(p); err == nil {
				sum := sha256.Sum256([]byte(target))
				e.linkSHA256 = hex.EncodeToString(sum[:])
			}
		case !typ.IsRegular():
			e.Kind = KindSpecial
		default:
			readFile(r, e, lim.MaxFileBytes)
		}
		if e.Kind == KindFile {
			total += int64(len(e.Data))
			if total > lim.MaxTotalBytes {
				return fmt.Errorf("more than %d bytes", lim.MaxTotalBytes)
			}
		}
		t.add(e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(t.Paths)
	t.byDir = map[string][]*Entry{}
	for _, p := range t.Paths {
		d := path.Dir(p)
		t.byDir[d] = append(t.byDir[d], t.Entries[p])
	}
	return t, nil
}

func (t *Tree) add(e *Entry) {
	t.Entries[e.Path] = e
	t.Paths = append(t.Paths, e.Path)
}

// readFile opens p without following a final symlink and without blocking on
// a fifo swapped in after the directory read, then checks it is regular.
func readFile(r *os.Root, e *Entry, max int64) {
	f, err := r.OpenFile(e.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		e.Kind = KindUnreadable
		return
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil:
		e.Kind = KindUnreadable
		return
	case !st.Mode().IsRegular():
		e.Kind = KindSpecial
		return
	case st.Size() > max:
		e.Kind = KindOversize
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		e.Kind = KindUnreadable
		return
	}
	if int64(len(data)) > max {
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
		return a.SHA256 == b.SHA256
	case KindSymlink:
		return a.linkSHA256 != "" && a.linkSHA256 == b.linkSHA256
	case KindSpecial:
		return true
	}
	return false // unreadable or oversize: unknown, so treated as changed
}
