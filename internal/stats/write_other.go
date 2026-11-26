//go:build !unix

package stats

import (
	"errors"
)

// WriteFile needs O_NOFOLLOW to refuse a link at the open itself, which
// this platform does not have, so a stats file is unsupported here.
func WriteFile(string, []byte, Guard) error {
	return errors.New("a stats file is not supported on this platform")
}

func usage() (user, system, maxRSS *int64) { return nil, nil, nil }

func cpuTime() *int64 { return nil }
