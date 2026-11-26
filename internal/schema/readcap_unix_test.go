//go:build unix

package schema

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestFifoHandoffIsRefused checks the open neither blocks on a fifo nor
// reads it.
func TestFifoHandoffIsRefused(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(fifo, nil, time.Time{}); err == nil {
		t.Error("fifo accepted")
	}
}
