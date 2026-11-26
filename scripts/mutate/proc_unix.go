//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in a process group of its own and, when its
// context ends, kills the whole group. go test runs the test binary as a
// child: killing the go command alone would leave that binary running.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
