package readcap

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.json")
	if err := os.WriteFile(file, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, p string
		limit   int64
		want    string // data, or the error's start
		err     bool
	}{
		{"at the cap", file, 5, "12345", false},
		{"over the cap", file, 4, "over 4 bytes", true},
		{"empty", empty, 5, "", false},
		{"link", link, 5, "not a regular file", true},
		{"fifo", fifo, 5, "not a regular file", true},
		{"directory", dir, 5, "not a regular file", true},
		{"missing", filepath.Join(dir, "none"), 5, "no such file or directory", true},
	} {
		data, err := File(c.p, c.limit)
		switch {
		case c.err && (err == nil || !strings.HasPrefix(err.Error(), c.want)):
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		case c.err && strings.Contains(err.Error(), dir):
			t.Errorf("%s: error %q holds the path", c.name, err)
		case !c.err && (err != nil || string(data) != c.want || data == nil):
			t.Errorf("%s: %q, %v", c.name, data, err)
		}
	}
	// Prefix gives a file over the cap as limit+1 bytes, no error.
	if data, err := Prefix(file, 3); err != nil || string(data) != "1234" {
		t.Errorf("prefix: %q, %v", data, err)
	}
}

// TestFileSocket checks a unix socket reads as not a regular file, whatever
// error the platform's open gives for it, and that the path is not printed.
func TestFileSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "rc") // short: a socket path has a low length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix socket: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	_, err = File(sock, 5)
	if err == nil || err.Error() != "not a regular file" {
		t.Errorf("socket: %v, want not a regular file", err)
	}
}
