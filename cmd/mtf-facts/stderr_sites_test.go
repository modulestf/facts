package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// forged is text a hostile input could carry into an error: a line break and
// a line that looks like ours.
const forged = "a\nmtf-facts: forged line"

// wantOneMtfLine fails unless got is exactly one "mtf-facts: " line, plus the
// usage hint when hint is set, and no line starts with a forged "mtf-facts:".
func wantOneMtfLine(t *testing.T, got string, hint bool) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	want := 1
	if hint {
		want = 2
	}
	if !strings.HasSuffix(got, "\n") || len(lines) != want || !strings.HasPrefix(lines[0], "mtf-facts: ") {
		t.Errorf("stderr is not %d line(s) led by \"mtf-facts: \": %q", want, got)
	}
	if hint && len(lines) == 2 && !strings.HasPrefix(lines[1], "Run '") {
		t.Errorf("second line is not the usage hint: %q", got)
	}
	if strings.Contains(got, "\nmtf-facts: forged") {
		t.Errorf("stderr holds a forged line: %q", got)
	}
}

// TestStderrIsOneLineSites covers the error sites TestStderrIsOneLine does
// not reach, each with the text of its error holding a line break. Removing
// the oneLine call of a site fails its case.
func TestStderrIsOneLineSites(t *testing.T) {
	// cobra's own error, written by run (main.go): an unknown flag carries
	// the argument back unquoted.
	t.Run("unknown flag", func(t *testing.T) {
		t.Parallel()
		code, out, errOut := runCLI([]string{"lint", "--" + forged})
		if code != exitError || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		wantOneMtfLine(t, errOut, true)
	})

	t.Run("usageError", func(t *testing.T) {
		t.Parallel()
		var errb bytes.Buffer
		cmd := &cobra.Command{Use: "x"}
		cmd.SetErr(&errb)
		if code := usageError(cmd, forged); code != exitError {
			t.Fatalf("exit %d", code)
		}
		wantOneMtfLine(t, errb.String(), true)
	})

	// runBounded's three error lines: the worker's error, the commit's
	// error and a note.
	for name, res := range map[string]workResult{
		"runBounded worker error": {err: errors.New(forged)},
		"runBounded commit error": {out: []byte("{}"), commit: func() error { return errors.New(forged) }},
		"runBounded note":         {out: []byte("{}"), notes: []string{"mtf-facts: " + forged}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			cmd := &cobra.Command{Use: "x"}
			cmd.SetOut(&out)
			cmd.SetErr(&errb)
			lim := facts.DefaultLimits
			runBounded(cmd, time.Minute, &lim, func() workResult { return res }, nil)
			wantOneMtfLine(t, errb.String(), false)
		})
	}

	// A bundle error from the worker, here a report path with a line break
	// in its name that the report reader names.
	t.Run("bundle error", func(t *testing.T) {
		t.Parallel()
		tmp := t.TempDir()
		sha := strings.Repeat("0", 40)
		code, _, errOut := runCLI([]string{
			"bundle", "--report", filepath.Join(tmp, forged), "--skills", tmp,
			"--out", filepath.Join(tmp, "out"), "--injection", filepath.Join(tmp, "inj"),
			"--head-sha", sha, "--merge-base-sha", sha, "--binary-sha256", strings.Repeat("0", 64),
		})
		if code != exitError {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		wantOneMtfLine(t, errOut, false)
	})

	// writeJSON: a value whose encoder fails with a line break in its error.
	t.Run("writeJSON", func(t *testing.T) {
		t.Parallel()
		var out, errb bytes.Buffer
		cmd := &cobra.Command{Use: "x"}
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if code := writeJSON(cmd, failingJSON{}); code != exitError {
			t.Fatalf("exit %d", code)
		}
		wantOneMtfLine(t, errb.String(), false)
	})

	// fixture-build: the --out check error carries the path, here a missing
	// one whose name has the break.
	t.Run("fixture-build out", func(t *testing.T) {
		t.Parallel()
		args := fixtureBuildArgs(t, "ladder-pass-01")
		args[5] = filepath.Join(t.TempDir(), forged)
		code, out, errOut := runCLI(args)
		if code != exitError || out != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
		wantOneMtfLine(t, errOut, false)
		if !strings.HasPrefix(errOut, "mtf-facts: fixture-build: ") {
			t.Errorf("not the fixture-build line: %q", errOut)
		}
	})

	// stats: the record cannot be written to a directory that does not
	// exist, and its name carries the break. The run itself is unaffected.
	t.Run("stats", func(t *testing.T) {
		t.Parallel()
		db := "../../testdata/diff-base"
		p := filepath.Join(t.TempDir(), forged, "stats.json")
		code, _, errOut := runCLI([]string{"affected", db + "/head", "--base", db + "/base", "--stats-file", p})
		if code != exitClean {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		wantOneMtfLine(t, errOut, false)
		if !strings.HasPrefix(errOut, "mtf-facts: stats: ") {
			t.Errorf("not the stats line: %q", errOut)
		}
	})
}

// TestStderrIsOneLineVersion covers the version report's error line. It
// replaces marshalVersion, so it does not run in parallel.
func TestStderrIsOneLineVersion(t *testing.T) {
	old := marshalVersion
	marshalVersion = func(any) ([]byte, error) { return nil, errors.New(forged) }
	t.Cleanup(func() { marshalVersion = old })
	code, out, errOut := runCLI([]string{"version", "--json"})
	if code != exitError || out != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	wantOneMtfLine(t, errOut, false)
	if !strings.HasPrefix(errOut, "mtf-facts: version: ") {
		t.Errorf("not the version line: %q", errOut)
	}
}

// failingJSON is a value whose JSON encoding fails with a line break in the
// error.
type failingJSON struct{}

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, errors.New(forged) }

// TestWriteOutputsQuotesPath checks that the error for an output that exists
// quotes the path, so a line break in its name stays out of the message.
func TestWriteOutputsQuotesPath(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	hostile := filepath.Join(tmp, forged)
	if err := os.WriteFile(hostile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeOutputs(hostile, filepath.Join(tmp, "inj"), nil, nil)
	if err == nil || strings.ContainsAny(err.Error(), "\n\r") {
		t.Errorf("error %q is missing or holds a line break", err)
	}
}
