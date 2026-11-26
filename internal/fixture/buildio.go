package fixture

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"syscall"
)

// Byte limits of the builder's input files.
const (
	MaxTriggerBytes  = maxTriggerBytes
	MaxTemplateBytes = maxTemplateBytes
)

// WriteFiles writes the files of a build under dir, which must be an
// existing empty directory and no link. Every write goes through an
// os.Root of dir, so no path leaves it, and each subdirectory and file is
// made new, never through a link. It stops at the first error.
func WriteFiles(dir string, files []File) error {
	if err := CheckOut(dir); err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := emptyDir(r, dir); err != nil {
		return err
	}
	made := map[string]bool{".": true}
	for _, f := range files {
		if sub := path.Dir(f.Path); !made[sub] {
			if err := r.Mkdir(sub, 0o777); err != nil {
				return err
			}
			made[sub] = true
		}
		h, err := r.OpenFile(f.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o666)
		if err != nil {
			return err
		}
		_, werr := h.Write(f.Data)
		if err := errors.Join(werr, h.Close()); err != nil {
			return err
		}
	}
	return nil
}

// CheckOut refuses an output directory that is missing, is not a
// directory, is a link or is not empty. The command checks it before the
// build, so a refusal is never reported for an --out it could not use.
func CheckOut(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	return emptyDir(r, dir)
}

// emptyDir refuses a root that holds any entry.
func emptyDir(r *os.Root, dir string) error {
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	if names, err := f.Readdirnames(1); len(names) > 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return errors.Join(fmt.Errorf("%s is not empty", dir), err)
	}
	return nil
}
