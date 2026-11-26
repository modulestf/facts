//go:build unix

package stats

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// euid is the effective user id; a test replaces it.
var euid = os.Geteuid

// beforeOpen runs between the Fstatat of the file in its parent directory
// and the Openat of it. Tests swap the path there; it is nil in production.
var beforeOpen func()

// WriteFile writes data to the file at p. It creates p with mode 0600 when
// p does not exist. An existing p must be a regular file with one link that
// the caller owns; it is then replaced. The parent directory of p is opened
// once, and the tree check and the open of the file both go through that
// descriptor, so a link or path component swapped after the check cannot
// move the file into an input tree. A directory moved into a tree after
// the check is outside that guarantee: it takes write access to the tree.
// The file is opened relative to it with O_NOFOLLOW and the checks are on
// the file opened, so a final link, or a file swapped between a check and
// the open, cannot slip through. O_NONBLOCK keeps a fifo from blocking the
// open. A path whose parent directory lies in one of
// g's input trees is refused before the open, and a file that is one of
// g's outputs or input files after it, so the record never overwrites a
// report or an input. Nothing is changed when a check fails.
func WriteFile(p string, data []byte, g Guard) error {
	if err := checkSize(data); err != nil {
		return err
	}
	if hasDotDot(p) {
		return fmt.Errorf("%s: holds a .. element", p)
	}
	dfd, base, err := openParent(p)
	if err != nil {
		return err
	}
	defer closeFd(dfd)
	if err := g.inTreeAt(p, dfd); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(dfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s: %w", p, errNotOwned)
	} else if err != nil && !errors.Is(err, unix.ENOENT) {
		return &fs.PathError{Op: "lstat", Path: p, Err: err}
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	fd, err := unix.Openat(dfd, base, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return &fs.PathError{Op: "open", Path: p, Err: err}
	}
	f := os.NewFile(uintptr(fd), p)
	if err := checkOpened(p, f, g); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Truncate(0); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// openParent opens the raw parent directory of p, the text before its last
// separator, "." for a p with none, which the kernel resolves with its
// links from the working directory, and returns its descriptor and the
// last element of p. A p that ends in a separator names no file and is
// refused.
func openParent(p string) (int, string, error) {
	dir, base := ".", p
	if i := strings.LastIndexByte(p, filepath.Separator); i >= 0 {
		dir, base = p[:i+1], p[i+1:] // keeps the separator, so "/" stays "/"
	}
	if base == "" || base == "." {
		return -1, "", fmt.Errorf("%s: %w", p, errNotOwned)
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	return dfd, base, nil
}

// inTreeAt refuses p when dfd, a descriptor of its parent directory, is an
// input tree root or lies inside one. The walk is physical and goes through
// descriptors alone, with no step on a path's text: it Fstats dfd, then
// opens its ".." relative to it, Fstats that, and so on up to the file
// system root, where ".." stops changing, closing each descriptor after the
// next one is open. Each directory is compared with every root, stat'ed
// once, by device and inode, so a link, a differently cased name on a
// case-insensitive file system, a firmlink or a bind mount of a root is the
// root, and a path of any depth stays within the system's limit. Opening a
// directory needs read permission on it, so a directory on the way that
// cannot be opened or stat'ed refuses p: the check fails closed.
func (g Guard) inTreeAt(p string, dfd int) error {
	var roots []unix.Stat_t
	for _, t := range g.Trees {
		var st unix.Stat_t
		if unix.Stat(t, &st) == nil {
			roots = append(roots, st)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	uncheckable := func(err error) error {
		return fmt.Errorf("%s: cannot check it against the input trees: %w", p, err)
	}
	same := func(a, b *unix.Stat_t) bool { return a.Dev == b.Dev && a.Ino == b.Ino }
	cur := dfd
	defer func() {
		if cur != dfd {
			closeFd(cur)
		}
	}()
	var prev *unix.Stat_t
	for {
		var st unix.Stat_t
		if err := unix.Fstat(cur, &st); err != nil {
			return uncheckable(err)
		}
		if prev != nil && same(&st, prev) {
			return nil // the root: its ".." is itself
		}
		if slices.ContainsFunc(roots, func(r unix.Stat_t) bool { return same(&st, &r) }) {
			return fmt.Errorf("%s: is inside an input tree of this run", p)
		}
		prev = &st
		next, err := unix.Openat(cur, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return uncheckable(err)
		}
		if cur != dfd {
			closeFd(cur)
		}
		cur = next
	}
}

// closeFd closes a directory descriptor opened only to read; an error
// closing it loses nothing.
func closeFd(fd int) { _ = unix.Close(fd) }

// checkOpened checks the file opened at p before anything is written.
func checkOpened(p string, f *os.File, g Guard) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() || st.Nlink != 1 || int(st.Uid) != euid() {
		return fmt.Errorf("%s: %w", p, errNotOwned)
	}
	if what := g.sameFile(fi); what != "" {
		return fmt.Errorf("%s: is %s", p, what)
	}
	return nil
}

// cpuTime returns the process CPU time, user and system, nil when the
// system call fails.
func cpuTime() *int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return nil
	}
	t := ru.Utime.Nano() + ru.Stime.Nano()
	return &t
}

// usage returns the process CPU times and peak resident set size, nil
// when the system call fails.
func usage() (user, system, maxRSS *int64) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return nil, nil, nil
	}
	u := ru.Utime.Nano()
	s := ru.Stime.Nano()
	// Maxrss is an int32 on 32-bit systems; widen it before the multiply.
	m := int64(ru.Maxrss) * maxRSSUnit //nolint:unconvert // int64 already on 64-bit systems
	return &u, &s, &m
}
