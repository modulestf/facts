package stats

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MaxRecordBytes bounds the record a command writes. A record is a few
// kilobytes: its lists are the fixed phases and one entry per rule.
const MaxRecordBytes = 1 << 20

// errNotOwned is the refusal of a path that is not a regular file the
// caller owns.
var errNotOwned = errors.New("not a regular file with one link that the caller owns")

// Guard names what a record must never overwrite or land in: the files the
// run's output goes to, the files it read and the trees it read.
type Guard struct {
	Outputs []*os.File // such as os.Stdout and os.Stderr
	Files   []string   // single input files, compared by identity
	Trees   []string   // input tree roots, compared by resolved path
}

// sameFile reports which of g's outputs and input files fi is, "" for none.
func (g Guard) sameFile(fi os.FileInfo) string {
	for _, o := range g.Outputs {
		if o == nil {
			continue
		}
		if ofi, err := o.Stat(); err == nil && os.SameFile(fi, ofi) {
			return "the file an output of this run goes to"
		}
	}
	for _, p := range g.Files {
		if ifi, err := os.Stat(p); err == nil && os.SameFile(fi, ifi) {
			return "an input file of this run"
		}
	}
	return ""
}

// inTree refuses p when its parent directory is an input tree root or lies
// inside one. Directories are compared by identity, not by path, so a
// differently cased name on a case-insensitive file system, a firmlink or a
// bind mount of a root is still the root. The parent is resolved first, so
// each step up is its physical parent; each step is compared with every
// root, stat'ed once. A parent or root that does not resolve is left to the
// open, which then fails or creates a file no tree holds.
func (g Guard) inTree(p string) error {
	var roots []os.FileInfo
	for _, t := range g.Trees {
		if fi, err := os.Stat(t); err == nil {
			roots = append(roots, fi)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil
	}
	for {
		if fi, err := os.Stat(dir); err == nil {
			for _, r := range roots {
				if os.SameFile(fi, r) {
					return fmt.Errorf("%s: is inside an input tree of this run", p)
				}
			}
		}
		up := filepath.Dir(dir)
		if up == dir {
			return nil
		}
		dir = up
	}
}

// checkSize refuses data over MaxRecordBytes.
func checkSize(data []byte) error {
	if len(data) > MaxRecordBytes {
		return fmt.Errorf("record of %d bytes is over %d", len(data), MaxRecordBytes)
	}
	return nil
}
