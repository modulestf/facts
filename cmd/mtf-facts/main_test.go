package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

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
	if !strings.Contains(out.String(), `variables.tf:5: unconfirmed structure.variable-untyped: variable "added"`) ||
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
