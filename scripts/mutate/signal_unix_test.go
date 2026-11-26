//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// SIGTERM, as a CI runner sends to cancel a job, ends the run and the test
// process of the mutant it was running.
func TestSigtermEndsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	bin := filepath.Join(t.TempDir(), "mutate")
	build := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // G204: builds this package into a temp directory
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	dir := t.TempDir()
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": slowTest})
	gitIn := func(args ...string) {
		t.Helper()
		//nolint:gosec // G204: fixed git arguments of a throwaway repository
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q")
	gitIn("add", ".")
	gitIn("commit", "-q", "-m", "base")
	// Touch the guard's line so the one mutant is made.
	edited := "package m\n\nfunc Pos(n int) int {\n\tif n < 0 { \n\t\treturn 0\n\t}\n\treturn n\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "m.go"), []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-base", "HEAD", "-budget", "4m", "-workers", "1") //nolint:gosec // G204: the binary this test built
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MUTATE_PIDFILE="+pidFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := waitForPID(t, pidFile)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mutate after SIGTERM: %v, want a clean exit with a report", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("mutate did not end after SIGTERM")
	}
	assertGone(t, pid)
}
