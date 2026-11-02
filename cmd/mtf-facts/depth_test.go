package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDepth runs depth on the diff-base trees without the classifier and
// affected reports: the change is deep, and the report has every key.
func TestDepth(t *testing.T) {
	const dir = "../../testdata/diff-base"
	code, out, errOut := runCLI([]string{"depth", "--head", dir + "/head", "--base", dir + "/base", "--files", dir + "/files.json"})
	if code != exitClean {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want := []string{"checks_out_of_scope", "depth", "examples_out_of_scope", "examples_selected", "format", "limits",
		"reasons", "schema_locks", "schema_providers", "schema_types", "touched_types", "walk"}
	if !slices.Equal(keys, want) {
		t.Errorf("keys %v", keys)
	}
	var r struct {
		Depth   string   `json:"depth"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Depth != "deep" || !slices.Contains(r.Reasons, "deep-classifier-unavailable") || !slices.Contains(r.Reasons, "deep-affected-fallback") {
		t.Errorf("report %+v", r)
	}
}

func TestDepthErrors(t *testing.T) {
	const dir = "../../testdata/diff-base"
	bad := filepath.Join(t.TempDir(), "files.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"depth"},
		{"depth", "--head", dir + "/head", "--base", dir + "/base"},
		{"depth", "--head", dir + "/nosuch", "--base", dir + "/base", "--files", dir + "/files.json"},
		{"depth", "--head", dir + "/head", "--base", dir + "/base", "--files", bad},
		{"depth", "--head", dir + "/head", "--base", dir + "/base", "--files", dir + "/files.json", "--examples", "../x"},
		{"depth", "--head", dir + "/head", "--base", dir + "/base", "--files", dir + "/files.json", "--timeout", "0s"},
		{"depth", "extra"},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
