// Package readcap reads one regular file under a byte cap, the one way every
// input file but a tree's is read.
package readcap

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// File reads the regular file at p, refusing a link, a special file, a
// directory and a file over limit bytes. Its errors hold no path: the caller
// names the file as it wants it printed.
func File(p string, limit int64) ([]byte, error) {
	data, err := Prefix(p, limit)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("over %d bytes", limit)
	}
	return data, nil
}

// Prefix is File for a caller that judges the size itself: a file over
// limit bytes is no error, and its first limit+1 bytes are returned, so it
// reads as too large rather than cut. The data is never nil without an
// error.
//
// The open itself refuses a final link, and the check is on the file opened,
// so a path swapped between a check and the open cannot slip through.
// O_NONBLOCK keeps a fifo from blocking the open.
func Prefix(p string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err // drop the path the caller did not ask to print
		}
		// O_NOFOLLOW refuses a link with ELOOP, and a socket fails with
		// ENXIO; any other error, such as a missing file, is kept as it is.
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENXIO) {
			return nil, errors.New("not a regular file")
		}
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		return nil, err
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}
