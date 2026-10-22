//go:build !unix

package schema

import "errors"

// readCapped needs O_NOFOLLOW to refuse a link at the open itself, which
// this platform does not have, so schema input is unsupported here.
func readCapped(string, int64) ([]byte, error) {
	return nil, errors.New("schema input is not supported on this platform")
}
