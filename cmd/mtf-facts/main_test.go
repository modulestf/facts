package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/facts"
)

const hangEnv = "MTF_FACTS_TEST_HANG"

// TestMain doubles as a helper process: with hangEnv set it runs the CLI
// with an engine that never returns, so the timeout path can be tested.
func TestMain(m *testing.M) {
	if os.Getenv(hangEnv) == "1" {
		runFacts = func(facts.Options) (*facts.Report, error) { select {} }
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestRules(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"rules"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	want := "examples.versions-missing\ttool\nstructure.missing-description\ttool\nstructure.variable-untyped\tcandidate\n"
	if out.String() != want {
		t.Errorf("got %q", out.String())
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != 2 || errb.Len() == 0 {
		t.Errorf("exit %d", code)
	}
}

func TestLint(t *testing.T) {
	head, base := "../../testdata/variable-untyped-base/head", "../../testdata/variable-untyped-base/base"
	var out, errb bytes.Buffer
	if code := run([]string{"lint", head, "--base", base}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `variables.tf:5: unconfirmed structure.variable-untyped: .:var.added`) ||
		!strings.Contains(out.String(), "registry sha256 none") {
		t.Errorf("human output:\n%s", out.String())
	}

	out.Reset()
	code := run([]string{"lint", "--json", "--registry", "../../README.md", "../../testdata/versions-missing-positive/head"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var r struct {
		Envelope struct {
			Tool struct {
				Version      string `json:"version"`
				BinarySHA256 string `json:"binary_sha256"`
			}
			RegistrySHA256 *string `json:"registry_sha256"`
		}
		Findings []struct {
			RuleID string `json:"rule_id"`
		}
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 2 || r.Envelope.RegistrySHA256 == nil || len(r.Envelope.Tool.BinarySHA256) != 64 || r.Envelope.Tool.Version == "" {
		t.Errorf("json output:\n%s", out.String())
	}
}

func TestLintErrors(t *testing.T) {
	for _, args := range [][]string{
		{"lint"},
		{"lint", "a", "b"},
		{"lint", "../../testdata/missing"},
		{"lint", "--head-sha", "xyz", "../../testdata/versions-missing-positive/head"},
		{"lint", "--bogus", "."},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestRulesMatchImplemented(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"rules"}, &out, &errb)
	var want strings.Builder
	for _, r := range facts.Rules {
		want.WriteString(r.ID + "\t" + r.Tier + "\n")
	}
	if out.String() != want.String() {
		t.Errorf("rules output %q, implemented %q", out.String(), want.String())
	}
}

func TestLintExitNotEvaluated(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"lint", "../../testdata/unparsable-areas/head"}, &out, &errb); code != exitNotEvaluated {
		t.Errorf("exit %d", code)
	}
	if code := run([]string{"lint", "--timeout", "1ns", "../../testdata/unparsable-areas/head"}, &out, &errb); code != exitError {
		t.Errorf("timeout exit %d", code)
	}
}

func TestHumanOutputEscapesTreeText(t *testing.T) {
	dir := t.TempDir()
	name := "x\x1b[2J\nmain.tf:1: CRITICAL fake.rule: forged.tf"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("variable \"v\" {\n  type = string\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"lint", dir}, &out, &errb); code != exitFindings {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	s := out.String()
	if strings.Contains(s, "\x1b") || strings.Contains(s, "\nmain.tf:1: CRITICAL") || !strings.Contains(s, `x\x1b[2J\x0amain.tf`) {
		t.Errorf("unescaped output:\n%s", s)
	}
	if got := clean(strings.Repeat("a", 1000)); len(got) != 303 {
		t.Errorf("cap: %d", len(got))
	}
}

func TestLintModulesLinkExitsNotEvaluated(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "modules")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"lint", dir}, &out, &errb); code != exitNotEvaluated {
		t.Errorf("exit %d:\n%s", code, out.String())
	}
}

func TestLintTimeoutStopsStuckEngine(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	//nolint:gosec // G204: re-runs this test binary as a helper process
	cmd := exec.CommandContext(ctx, exe, "lint", "../../testdata/versions-missing-positive/head", "--timeout", "200ms")
	cmd.Env = append(os.Environ(), hangEnv+"=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("outer deadline expired: %v", ctx.Err())
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != exitError {
		t.Fatalf("want exit %d, got %v", exitError, err)
	}
	if errb.String() != "mtf-facts: timeout\n" {
		t.Errorf("stderr %q", errb.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout %q", out.String())
	}
}

func TestLintRejectsNonPositiveTimeout(t *testing.T) {
	for _, v := range []string{"0", "-1s"} {
		var out, errb bytes.Buffer
		if code := run([]string{"lint", "--timeout", v, "../../testdata/versions-missing-positive/head"}, &out, &errb); code != exitError {
			t.Errorf("--timeout %s: exit %d", v, code)
		}
		if out.Len() != 0 || !strings.Contains(errb.String(), "--timeout must be greater than zero") {
			t.Errorf("--timeout %s: stdout %q stderr %q", v, out.String(), errb.String())
		}
	}
}

func TestLintEngineDeadlineIsTimeout(t *testing.T) {
	orig := runFacts
	runFacts = func(facts.Options) (*facts.Report, error) {
		return nil, fmt.Errorf("head tree: %w", facts.ErrDeadline)
	}
	t.Cleanup(func() { runFacts = orig })
	var out, errb bytes.Buffer
	if code := run([]string{"lint", "../../testdata/versions-missing-positive/head"}, &out, &errb); code != exitError {
		t.Errorf("exit %d", code)
	}
	if errb.String() != "mtf-facts: timeout\n" || out.Len() != 0 {
		t.Errorf("stdout %q stderr %q", out.String(), errb.String())
	}
}
