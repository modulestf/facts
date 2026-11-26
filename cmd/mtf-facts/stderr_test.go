package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOneLine(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"plain":           "plain",
		"a\nb":            `a\x0ab`,
		"a\rb\tc":         `a\x0db\x09c`,
		"\x1b[31m":        `\x1b[31m`,
		"x\x7fy":          `x\x7fy`,
		"caf\u00e9 ok":    "caf\u00e9 ok",
		"\x00end":         `\x00end`,
		"line1\n\nline2 ": `line1\x0a\x0aline2 `,
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestStderrIsOneLine: a command error that names a file with a line break
// in its name is exactly one line on stderr, whatever the error sites did
// with the name, so no input can forge an "mtf-facts:" line. The names are
// paths the command was given and the file system reports, which reach the
// error unquoted from the standard library.
func TestStderrIsOneLine(t *testing.T) {
	t.Parallel()
	hostile := filepath.Join(t.TempDir(), "a\nmtf-facts: forged line.json")
	td := filepath.Join("..", "..", "testdata")
	module := filepath.Join(td, "ledger", "module")
	// A tree whose only .tf file has a line break in its name and does not
	// parse, and one whose only .tf file is a link: the reason the tree is
	// not read names the file.
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "a\nmtf-facts: forged line.tf"), []byte("resource \"x\" \"y\" {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := t.TempDir()
	if err := os.Symlink("main.tf", filepath.Join(linked, "a\nmtf-facts: forged line.tf")); err != nil {
		t.Fatal(err)
	}
	schema := filepath.Join(td, "ledger", "aws.json")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"ledger", []string{"ledger", module, "--schema-files", hostile}},
		{"ledger tree parse error", []string{"ledger", broken, "--schema-files", schema}},
		{"ledger tree link", []string{"ledger", linked, "--schema-files", schema}},
		{"schema-diff", []string{"schema-diff", "--from", hostile, "--to", filepath.Join(td, "schema-diff", "to.json"), "--types-from", filepath.Join(td, "schema-diff", "module")}},
		{"schema-diff tree parse error", []string{"schema-diff", "--from", filepath.Join(td, "schema-diff", "from.json"), "--to", filepath.Join(td, "schema-diff", "to.json"), "--types-from", broken}},
		{"schema-diff tree link", []string{"schema-diff", "--from", filepath.Join(td, "schema-diff", "from.json"), "--to", filepath.Join(td, "schema-diff", "to.json"), "--types-from", linked}},
		{"lint", []string{"lint", module, "--schema-files", hostile}},
		{"affected", []string{"affected", module, "--diff", hostile}},
		{"changes", []string{"changes", module, "--diff", hostile}},
		{"depth", []string{"depth", "--base", module, "--head", module, "--files", hostile}},
		{"profile-snapshot", []string{"profile-snapshot", "--profile-dir", hostile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != exitError {
				t.Fatalf("exit %d, want %d; stderr %q", code, exitError, errb.String())
			}
			got := errb.String()
			if !strings.HasPrefix(got, "mtf-facts: ") || strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Errorf("stderr is not one mtf-facts line: %q", got)
			}
			if strings.Contains(got, "\nmtf-facts: forged") {
				t.Errorf("stderr holds a forged line: %q", got)
			}
		})
	}
}
