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
	"slices"
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
	// The listing is every implemented rule in id order, with its tier.
	var want strings.Builder
	for _, r := range facts.Rules {
		want.WriteString(r.ID + "\t" + r.Tier + "\n")
	}
	if !slices.IsSortedFunc(facts.Rules, func(a, b facts.Rule) int { return strings.Compare(a.ID, b.ID) }) {
		t.Error("facts.Rules is not sorted by id")
	}
	if out.String() != want.String() {
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
	if code := run([]string{"lint", head, "--base", base, "--profile-dir", "../../testdata/profile"}, &out, &errb); code != 0 {
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
	// The same base, profile and module with a parse error and without: the
	// valid head exits 0, so the parse error alone gives the exit code.
	base, good, dir := t.TempDir(), t.TempDir(), t.TempDir()
	for d, body := range map[string]string{base: "# a\n", good: "# b\n", dir: "variable \"x\" {\n"} {
		if err := os.WriteFile(filepath.Join(d, "main.tf"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flags := []string{"--base", base, "--profile-dir", "../../testdata/profile"}
	var out, errb bytes.Buffer
	if code := run(append([]string{"lint", good}, flags...), &out, &errb); code != 0 {
		t.Fatalf("valid tree: exit %d: %s", code, out.String())
	}
	out.Reset()
	if code := run(append([]string{"lint", dir}, flags...), &out, &errb); code != exitNotEvaluated {
		t.Errorf("exit %d: %s", code, out.String())
	}
	if code := run(append([]string{"lint", "--timeout", "1ns", dir}, flags...), &out, &errb); code != exitError {
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

func TestStubSubcommandsExit2(t *testing.T) {
	for _, name := range []string{"merge"} {
		var out, errb bytes.Buffer
		if code := run([]string{name, "x"}, &out, &errb); code != exitError || out.Len() != 0 ||
			!strings.Contains(errb.String(), name+" is not implemented") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out.String(), errb.String())
		}
	}
}

// TestLintHookFlagsChangeNoOutput passes a schema handoff, which no
// implemented rule reads, and checks lint writes the same human output and
// exit code as without it. Both runs pass the test profile, which rules read.
func TestLintHookFlagsChangeNoOutput(t *testing.T) {
	hooks := []string{"--schema-handoff", "../../schema/examples/schema-handoff.valid.json"}
	profile := []string{"--profile-dir", "../../testdata/profile"}
	for _, args := range [][]string{
		append([]string{"lint", "../../testdata/variable-untyped-base/head", "--base", "../../testdata/variable-untyped-base/base"}, profile...),
		append([]string{"lint", "../../testdata/versions-missing-positive/head"}, profile...),
		append([]string{"lint", "../../testdata/unparsable-areas/head"}, profile...),
	} {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		var hout, herrb bytes.Buffer
		hcode := run(append(slices.Clone(args), hooks...), &hout, &herrb)
		if code != hcode || out.String() != hout.String() || errb.String() != herrb.String() {
			t.Errorf("%v: exit %d vs %d, output differs:\n%s\n---\n%s", args, code, hcode, out.String(), hout.String())
		}
	}
}

// TestLintSchemaFlagsFailClosed checks a missing or wrong schema input exits
// 2 with nothing on stdout, and a good one is read.
func TestLintSchemaFlagsFailClosed(t *testing.T) {
	head := "../../testdata/versions-missing-positive/head"
	good := "../../schema/examples/schema-handoff.valid.json"
	raw := []byte(`{"format_version": "1.0", "provider_schemas": {}}`)
	same := []string{filepath.Join(t.TempDir(), "aws.json"), filepath.Join(t.TempDir(), "aws.json")}
	for _, p := range same {
		if err := os.WriteFile(p, raw, 0o600); err != nil { //nolint:gosec // G703: a file in the test temp directory
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		flags []string
		code  int
	}{
		"good handoff":        {[]string{"--schema-handoff", good}, exitFindings},
		"missing handoff":     {[]string{"--schema-handoff", "missing.json"}, exitError},
		"directory handoff":   {[]string{"--schema-handoff", "../../schema"}, exitError},
		"malformed handoff":   {[]string{"--schema-handoff", "../../schema/examples/schema-handoff.invalid.oneOf.json"}, exitError},
		"missing file":        {[]string{"--schema-files", "missing.json"}, exitError},
		"handoff as file":     {[]string{"--schema-files", good}, exitError},
		"second file missing": {[]string{"--schema-handoff", good, "--schema-files", "missing.json"}, exitError},
		"same base name":      {[]string{"--schema-files", same[0], "--schema-files", same[1]}, exitError},
	} {
		var out, errb bytes.Buffer
		code := run(append([]string{"lint", "--json", head}, c.flags...), &out, &errb)
		if code != c.code || (code == exitError) != (out.Len() == 0) {
			t.Errorf("%s: exit %d, stdout %d bytes, stderr %q", name, code, out.Len(), errb.String())
		}
		if code == exitFindings && !strings.Contains(out.String(), `"handoff_sha256"`) {
			t.Errorf("%s: no schema_inputs in the report", name)
		}
	}
}

// TestLintProfileDir checks a --profile-dir that is not a directory exits 2
// with nothing on stdout, and the test profile is read: its hash is in the
// JSON envelope and the exit code is the one without it.
func TestLintProfileDir(t *testing.T) {
	head := "../../testdata/versions-missing-positive/head"
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs("../../testdata/profile")
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(abs, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(tmp, "missing"), file, link} {
		var out, errb bytes.Buffer
		if code := run([]string{"lint", head, "--profile-dir", dir}, &out, &errb); code != exitError || out.Len() != 0 ||
			!strings.Contains(errb.String(), "is not a directory") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", filepath.Base(dir), code, out.String(), errb.String())
		}
	}
	var plain, out, errb bytes.Buffer
	want := run([]string{"lint", "--json", head}, &plain, &errb)
	code := run([]string{"lint", "--json", head, "--profile-dir", "../../testdata/profile"}, &out, &errb)
	var r struct {
		Envelope struct {
			ProfileSHA256 *string `json:"profile_sha256"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if code != want || r.Envelope.ProfileSHA256 == nil || len(*r.Envelope.ProfileSHA256) != 64 {
		t.Errorf("exit %d (want %d), profile_sha256 %v", code, want, r.Envelope.ProfileSHA256)
	}
}
