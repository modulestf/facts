//go:build unix

package main

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// assertGone fails when the process is still running a few seconds on.
func assertGone(t *testing.T, pid int) {
	t.Helper()
	for range 100 {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("process %d is still running", pid)
}
