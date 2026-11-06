// Package snapshot copies a module profile directory into a private
// temporary directory, so a host reads the options, the gates and the
// profile from one fixed copy. It follows no link, opens no special file and
// bounds what it reads.
package snapshot

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"syscall"

	"github.com/modulestf/facts/internal/profile"
)

// Bounds of one snapshot: directory entries of every kind, bytes of every
// file together, and path components of an entry below the source.
const (
	maxEntries = 256
	maxBytes   = 8 << 20
	maxDepth   = 8
)

// ErrRefused is returned for a source the snapshot will not copy: a link, a
// special file, an entry on another device, or a tree over a bound. Its
// message names paths relative to the source only.
var ErrRefused = errors.New("profile snapshot refused")

// Snapshot is a copy of a profile directory. TreeSHA256 is
// profile.TreeSHA256 of every file; ProfileSHA256 is the profile's own hash,
// nil when the copy is not a valid profile.
type Snapshot struct {
	Dir           string  `json:"dir"`
	TreeSHA256    string  `json:"tree_sha256"`
	ProfileSHA256 *string `json:"profile_sha256"`
}

// devOf returns the device of a file; a test replaces it.
var devOf = func(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev) //nolint:gosec,unconvert // Dev is int32 on darwin, uint64 on linux; a device number is never negative
	}
	return 0
}

// Take copies src into a new 0700 temporary directory and checks the copy
// reads back the same. Each directory is opened relative to its parent's
// descriptor and each file is opened, checked as the regular file the
// listing saw, on the source's device, and read from that one descriptor.
func Take(src string) (*Snapshot, error) {
	files, dirs, err := read(src)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "mtf-profile-")
	if err != nil {
		return nil, err
	}
	s, err := write(dir, files, dirs)
	if err != nil {
		_ = os.RemoveAll(dir) // a partial copy is never left behind
		return nil, err
	}
	return s, nil
}

// write writes the copy into dir, then reads it back and compares.
func write(dir string, files map[string][]byte, dirs []string) (*Snapshot, error) {
	// MkdirTemp made dir 0700.
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, d := range dirs {
		if err := r.Mkdir(d, 0o700); err != nil {
			return nil, err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return nil, err
		}
		_, werr := f.Write(files[name])
		if cerr := f.Close(); werr != nil || cerr != nil {
			return nil, errors.Join(werr, cerr)
		}
	}
	back, _, err := read(dir)
	if err != nil {
		return nil, fmt.Errorf("snapshot changed after the copy: %w", err)
	}
	sum := profile.TreeSHA256(files)
	if profile.TreeSHA256(back) != sum {
		return nil, fmt.Errorf("%w: the copy does not read back the same", ErrRefused)
	}
	s := &Snapshot{Dir: dir, TreeSHA256: sum}
	if p, err := profile.New(files); err == nil {
		s.ProfileSHA256 = &p.SHA256
	}
	return s, nil
}

// reader holds the state of one read of a tree.
type reader struct {
	dev     uint64
	entries int
	bytes   int64
	files   map[string][]byte
	dirs    []string
}

// read reads every file under src, keyed by slash path relative to it, and
// lists its directories, parents first.
func read(src string) (map[string][]byte, []string, error) {
	before, err := os.Lstat(src)
	if err != nil {
		return nil, nil, fmt.Errorf("profile directory: %w", errors.Unwrap(err))
	}
	if !before.IsDir() {
		return nil, nil, fmt.Errorf("%w: the profile directory is not a directory", ErrRefused)
	}
	r, err := os.OpenRoot(src)
	if err != nil {
		return nil, nil, fmt.Errorf("profile directory: %w", errors.Unwrap(err))
	}
	defer r.Close()
	after, err := r.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return nil, nil, fmt.Errorf("%w: the profile directory changed while it was opened", ErrRefused)
	}
	w := &reader{dev: devOf(after), files: map[string][]byte{}}
	if err := w.walk(r, "."); err != nil {
		return nil, nil, err
	}
	return w.files, w.dirs, nil
}

// walk reads the directory r, which is rel below the source.
func (w *reader) walk(r *os.Root, rel string) error {
	d, err := r.Open(".")
	if err != nil {
		return refused(rel, "cannot be listed")
	}
	ents, err := d.ReadDir(maxEntries + 1 - w.entries)
	_ = d.Close() // read-only
	if err != nil && !errors.Is(err, io.EOF) {
		return refused(rel, "cannot be listed")
	}
	if w.entries += len(ents); w.entries > maxEntries {
		return fmt.Errorf("%w: more than %d entries", ErrRefused, maxEntries)
	}
	for _, e := range ents {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		p := path.Join(rel, name)
		if strings.Count(p, "/")+1 > maxDepth {
			return refused(p, fmt.Sprintf("is more than %d levels deep", maxDepth))
		}
		if err := w.entry(r, name, p); err != nil {
			return err
		}
	}
	return nil
}

// entry reads one directory entry of r.
func (w *reader) entry(r *os.Root, name, p string) error {
	info, err := r.Lstat(name)
	switch {
	case err != nil:
		return refused(p, "cannot be read")
	case info.IsDir():
		sub, err := r.OpenRoot(name)
		if err != nil {
			return refused(p, "cannot be opened")
		}
		defer sub.Close()
		st, err := sub.Stat(".")
		if err != nil || !os.SameFile(info, st) {
			return refused(p, "changed while it was opened")
		}
		if devOf(st) != w.dev {
			return refused(p, "is on another device")
		}
		w.dirs = append(w.dirs, p)
		return w.walk(sub, p)
	case !info.Mode().IsRegular():
		return refused(p, "is a link or a special file")
	}
	// O_NONBLOCK keeps a fifo swapped in after the Lstat from blocking.
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return refused(p, "cannot be opened")
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil || !st.Mode().IsRegular() || !os.SameFile(info, st):
		return refused(p, "changed while it was opened")
	case devOf(st) != w.dev:
		return refused(p, "is on another device")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1-w.bytes))
	if err != nil {
		return refused(p, "cannot be read")
	}
	if w.bytes += int64(len(data)); w.bytes > maxBytes {
		return fmt.Errorf("%w: more than %d bytes", ErrRefused, maxBytes)
	}
	w.files[p] = data
	return nil
}

func refused(p, why string) error {
	return fmt.Errorf("%w: %s %s", ErrRefused, p, why)
}
