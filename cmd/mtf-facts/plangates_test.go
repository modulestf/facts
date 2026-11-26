package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planGatesTree writes a clone with one file that runs code, sets every
// run's standard input to its path, and returns the clone.
func planGatesTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "main.tf")
	if err := os.WriteFile(f, []byte("resource \"aws_s3_bucket\" \"this\" {\n  provisioner \"local-exec\" {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setStdin(t, f+"\n")
	return root
}

func setStdin(t *testing.T, s string) {
	t.Helper()
	old := testStdin
	testStdin = func() io.Reader { return strings.NewReader(s) }
	t.Cleanup(func() { testStdin = old })
}

func TestPlanGatesCLI(t *testing.T) {
	root := planGatesTree(t)
	main := filepath.Join(root, "main.tf")
	base := []string{"plan-gates", "--scope", "clone", "--mode", "laptop", "--root", root}
	code, out, errOut := runCLI(base)
	if code != exitFindings || out != strings.Repeat("G3 "+main+":2\n", 2) || errOut != "" {
		t.Errorf("hit: exit %d, %q, %q", code, out, errOut)
	}
	clean := filepath.Join(root, "clean.tf")
	if err := os.WriteFile(clean, []byte("resource \"aws_s3_bucket\" \"this\" {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setStdin(t, clean)
	if code, out, errOut := runCLI(base); code != exitClean || out != "" || errOut != "" {
		t.Errorf("clean: exit %d, %q, %q", code, out, errOut)
	}
	setStdin(t, "")
	if code, out, _ := runCLI(base); code != exitClean || out != "" {
		t.Errorf("no files: exit %d, %q", code, out)
	}
	setStdin(t, "UNREADABLE\n"+filepath.Join(root, "none.tf")+"\n")
	if code, out, _ := runCLI(base); code != exitFindings || out != "G0 UNREADABLE:0\nG0 "+filepath.Join(root, "none.tf")+":0\n" {
		t.Errorf("unreadable: exit %d, %q", code, out)
	}
	big := filepath.Join(root, "big.tf")
	if err := os.WriteFile(big, []byte(strings.Repeat("#\n", 4<<20+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	setStdin(t, big)
	if code, out, _ := runCLI(base); code != exitFindings || out != "G0 "+big+":0\n" {
		t.Errorf("over the file cap: exit %d, %q", code, out)
	}
	setStdin(t, strings.Repeat(clean+"\n", 10001))
	if code, out, errOut := runCLI(base); code != exitError || out != "" || !strings.Contains(errOut, "over 10000 files") {
		t.Errorf("too many files: exit %d, %q, %q", code, out, errOut)
	}
	setStdin(t, "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"plan-gates", "--mode", "laptop", "--root", root}, "--scope must be"},
		{[]string{"plan-gates", "--scope", "clone", "--mode", "cloud", "--root", root}, "--mode must be"},
		{[]string{"plan-gates", "--scope", "clone", "--mode", "laptop"}, "--root is required"},
		{[]string{"plan-gates", "--scope", "clone", "--mode", "runner", "--root", root, "--provider", "Bad/*"}, "not ns/name or ns/*"},
		{[]string{"plan-gates", "--scope", "clone", "--mode", "laptop", "--root", root, "extra"}, "unknown command"},
	} {
		code, out, errOut := runCLI(c.args)
		if code != exitError || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%v: exit %d, %q, %q", c.args, code, out, errOut)
		}
	}
}

// failWriter fails every write, as a full disk or a read-only descriptor
// does.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// TestPlanGatesWriteFailure checks hits that cannot be written exit 2, not
// 1. A closed standard output is no such case: the Go runtime opens
// /dev/null on a closed descriptor 0 to 2 before main runs, so the hits are
// written and discarded, as with >/dev/null, and the exit is the result's.
func TestPlanGatesWriteFailure(t *testing.T) {
	root := planGatesTree(t)
	var errb strings.Builder
	code := run([]string{"plan-gates", "--scope", "clone", "--mode", "laptop", "--root", root}, failWriter{}, &errb)
	if code != exitError {
		t.Errorf("exit %d, want %d; stderr %q", code, exitError, errb.String())
	}
}
