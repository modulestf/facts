package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
