//go:build unix

package schema

import (
	"path/filepath"
	"syscall"
	"testing"
)

// TestFifoHandoffIsRefused checks the open neither blocks on a fifo nor
// reads it.
func TestFifoHandoffIsRefused(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(fifo, nil); err == nil {
		t.Error("fifo accepted")
	}
}
