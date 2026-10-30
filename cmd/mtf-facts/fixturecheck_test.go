package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/fixture"
)

const fixtureTD = "../../testdata/fixture-check"

func TestFixtureCheckPasses(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{filepath.Join(fixtureTD, "l1"), "--json"},
		{filepath.Join(fixtureTD, "l3"), "--level", "L3", "--resource", "aws_rds_cluster", "--pins", filepath.Join(fixtureTD, "pins.json"), "--json"},
		{filepath.Join(fixtureTD, "l2"), "--level", "L2", "--pins", filepath.Join(fixtureTD, "pins.json"), "--expect", filepath.Join(fixtureTD, "l2.expect.json"), "--schema", filepath.Join(fixtureTD, "schema.json"), "--json"},
	} {
		code, out, errOut := runCLI(append([]string{"fixture-check"}, args...))
		if code != exitClean || errOut != "" {
			t.Fatalf("%v: exit %d, stderr %q, stdout %s", args, code, errOut, out)
		}
		var r fixture.Result
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if !r.OK || r.SchemaVersion != 1 || len(r.TreeSHA256) != 64 || r.Violations == nil || len(r.Violations) != 0 {
			t.Errorf("%v: %+v", args, r)
		}
		if got := strings.Contains(out, `"override_during_plan": true`); got != slices.Contains(args, "L2") {
			t.Errorf("%v: override_during_plan in output %v", args, got)
		}
		if !strings.Contains(out, `"violations": []`) {
			t.Errorf("%v: violations is not an empty list: %s", args, out)
		}
	}
}

func TestFixtureCheckViolations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("locals {\n  a = path.module\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI([]string{"fixture-check", dir, "--json"})
	var r fixture.Result
	if code != exitFindings || json.Unmarshal([]byte(out), &r) != nil || r.OK ||
		len(r.Violations) != 1 || r.Violations[0] != (fixture.Violation{File: "main.tf", Line: 2, Rule: fixture.RuleExprReferenceDenied, Detail: "path"}) {
		t.Errorf("exit %d, %s", code, out)
	}
	code, out, _ = runCLI([]string{"fixture-check", dir})
	if code != exitFindings || !strings.HasPrefix(out, "main.tf:2: expr.reference-denied path\n1 violations, tree sha256 ") {
		t.Errorf("text: exit %d, %q", code, out)
	}
}

func TestFixtureCheckExpect(t *testing.T) {
	t.Parallel()
	l1, m := filepath.Join(fixtureTD, "l1"), filepath.Join(fixtureTD, "l1.expect.json")
	if code, out, errOut := runCLI([]string{"fixture-check", l1, "--expect", m, "--json"}); code != exitClean || errOut != "" {
		t.Errorf("exit %d, stderr %q, stdout %s", code, errOut, out)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("output \"x\" {\n  value = 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI([]string{"fixture-check", dir, "--expect", m, "--json"})
	if code != exitFindings || !strings.Contains(out, `"rule": "manifest.block-set"`) {
		t.Errorf("other blocks: exit %d, %s", code, out)
	}
	if code, out, errOut := runCLI([]string{"fixture-check", l1, "--expect", filepath.Join(fixtureTD, "pins.json")}); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: fixture-check: expect:") {
		t.Errorf("bad manifest: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestFixtureCheckErrors(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(bad, []byte(`{"providers":[{"source":"hashicorp/aws","version":"~> 6"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	l1, l3 := filepath.Join(fixtureTD, "l1"), filepath.Join(fixtureTD, "l3")
	for _, args := range [][]string{
		{},
		{l1, l3},
		{filepath.Join(fixtureTD, "none")},
		{l1, "--level", "L4"},
		{l1, "--level", "L2", "--resource", "aws_rds_cluster"},
		{l1, "--schema", filepath.Join(fixtureTD, "schema.json")},
		{filepath.Join(fixtureTD, "l2"), "--level", "L2", "--schema", filepath.Join(fixtureTD, "pins.json")},
		{filepath.Join(fixtureTD, "l2"), "--level", "L2", "--schema", filepath.Join(fixtureTD, "none.json")},
		{l3, "--level", "L3"},
		{l1, "--resource", "aws_rds_cluster"},
		{l3, "--level", "L3", "--resource", "aws_rds_cluster", "--pins", bad},
		{l3, "--level", "L3", "--resource", "aws_rds_cluster", "--pins", filepath.Join(fixtureTD, "none.json")},
	} {
		code, out, errOut := runCLI(append([]string{"fixture-check"}, args...))
		if code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
