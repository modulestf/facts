//go:build unix

package schema

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// readCapped reads the regular file at p, refusing a link, a special file,
// a directory and anything over limit bytes. The open itself refuses a final
// link, and the check is on the file opened, so a path swapped between a
// check and the open cannot slip through. O_NONBLOCK keeps a fifo from
// blocking the open.
func readCapped(p string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("%s is not a regular file", p), err)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", p, limit)
	}
	return data, nil
}
