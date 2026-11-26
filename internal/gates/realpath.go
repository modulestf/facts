package gates

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
)


// realpath resolves p as the BSD realpath of Perl's Cwd does: every link is
// followed, "." and ".." are read on the resolved path, and a missing last
// component is no error, so the path it would have is returned. A missing
// directory on the way, a component that is no directory, or too many links
// is. A relative p is read from the process's physical working directory.
func realpath(p string) (string, bool) {
	var resolved, remaining string
	if strings.HasPrefix(p, "/") {
		if p == "/" {
			return "/", true
		}
		resolved, remaining = "/", p[1:]
	} else {
		wd, err := syscall.Getwd()
		if err != nil {
			return "", false
		}
		resolved, remaining = wd, p
	}
	links := 0
	for remaining != "" {
		name, last := remaining, true
		if i := strings.IndexByte(remaining, '/'); i >= 0 {
			name, remaining, last = remaining[:i], remaining[i+1:], false
		} else {
			remaining = ""
		}
		if !strings.HasSuffix(resolved, "/") {
			resolved += "/"
		}
		switch name {
		case "", ".":
			continue
		case "..":
			resolved = parentOf(resolved)
			continue
		}
		resolved += name
		fi, err := os.Lstat(resolved)
		if err != nil {
			if last && errors.Is(err, syscall.ENOENT) {
				return resolved, true
			}
			return "", false
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		if links > maxSymlinks {
			return "", false
		}
		links++
		target, err := os.Readlink(resolved)
		if err != nil {
			return "", false
		}
		if strings.HasPrefix(target, "/") {
			resolved = "/"
		} else {
			resolved = parentOf(resolved + "/")
		}
		if !last {
			if !strings.HasSuffix(target, "/") {
				target += "/"
			}
			target += remaining
		}
		remaining = target
	}
	if len(resolved) > 1 && strings.HasSuffix(resolved, "/") {
		resolved = resolved[:len(resolved)-1]
	}
	return resolved, true
}

// parentOf drops the last component of r, which ends in a slash, and keeps
// the slash before it; the root stays the root.
func parentOf(r string) string {
	if len(r) <= 1 {
		return r
	}
	t := r[:len(r)-1]
	return t[:strings.LastIndexByte(t, '/')+1]
}
