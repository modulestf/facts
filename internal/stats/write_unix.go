//go:build unix

package stats

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// euid is the effective user id; a test replaces it.
var euid = os.Geteuid

// beforeOpen runs between the Lstat of the path and its open. Tests swap
// the path there; it is nil in production.
var beforeOpen func()

// WriteFile writes data to the file at p. It creates p with mode 0600 when
// p does not exist. An existing p must be a regular file with one link that
// the caller owns; it is then replaced. It never follows a link: the open
// refuses a final link and the checks are on the file opened, so a path
// swapped between a check and the open cannot slip through. O_NONBLOCK
// keeps a fifo from blocking the open. A path whose parent directory lies
// in one of g's input trees is refused before the open, and a file that is
// one of g's outputs or input files after it, so the record never
// overwrites a report or an input. Nothing is changed when a check fails.
func WriteFile(p string, data []byte, g Guard) error {
	if err := checkSize(data); err != nil {
		return err
	}
	if err := g.inTree(p); err != nil {
		return err
	}
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s: %w", p, errNotOwned)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
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
	m := ru.Maxrss * maxRSSUnit
	return &u, &s, &m
}
