package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	ledgerModule = "../../testdata/ledger/module"
	ledgerSchema = "../../testdata/ledger/aws.json"
)

func TestLedger(t *testing.T) {
	// The schema and ledger are copied to a temp dir, so the test can check
	// that no directory of a path given reaches the output.
	dir := t.TempDir()
	copyFile := func(src string) string {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, filepath.Base(src))
		if err := os.WriteFile(p, data, 0o600); err != nil { //nolint:gosec // G703: p is a file of the test temp dir
			t.Fatal(err)
		}
		return p
	}
	schemaFile := copyFile(ledgerSchema)
	valid := copyFile("../../testdata/ledger/valid.json")
	registry := filepath.Join(dir, "rule-facts.md")
	if err := os.WriteFile(registry, []byte("registry\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		args     []string
		code     int
		problems int // -1: null
		ledger   string
	}{
		{name: "inventory only", args: []string{ledgerModule, "--schema-files", schemaFile}, code: exitClean, problems: -1},
		{name: "valid ledger", args: []string{ledgerModule, "--schema-files", schemaFile, "--check", valid, "--registry", registry}, code: exitClean, ledger: valid},
		{name: "unmapped field", args: []string{"--check", copyFile("../../testdata/ledger/unmapped.json"), "--schema-files", schemaFile, ledgerModule}, code: exitFindings, problems: 1, ledger: filepath.Join(dir, "unmapped.json")},
		{name: "half-exposed nested block", args: []string{ledgerModule, "--schema-files", schemaFile, "--check", copyFile("../../testdata/ledger/half-exposed.json")}, code: exitFindings, problems: 1, ledger: filepath.Join(dir, "half-exposed.json")},
		{name: "duplicate disposition", args: []string{ledgerModule, "--schema-files", schemaFile, "--check", copyFile("../../testdata/ledger/duplicate.json")}, code: exitFindings, problems: 1, ledger: filepath.Join(dir, "duplicate.json")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(append([]string{"ledger"}, tc.args...), &out, &errb); code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, errb.String())
			}
			if strings.Contains(out.String(), dir) {
				t.Error("a host path reached the output")
			}
			var rep struct {
				Envelope struct {
					Tool           struct{ Name, Version string }
					RegistrySHA256 *string                         `json:"registry_sha256"`
					SchemaFiles    []struct{ Path, SHA256 string } `json:"schema_files"`
					Ledger         *struct{ Path, SHA256 string }
				}
				Types    []json.RawMessage
				Problems *[]json.RawMessage
			}
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatal(err)
			}
			if rep.Envelope.Tool.Name != "mtf-facts" || rep.Envelope.Tool.Version == "" || len(rep.Types) != 2 {
				t.Errorf("envelope %+v, %d types", rep.Envelope, len(rep.Types))
			}
			if f := rep.Envelope.SchemaFiles; len(f) != 1 || f[0].Path != "aws.json" || f[0].SHA256 != fileSHA256(t, schemaFile) {
				t.Errorf("schema files %+v", f)
			}
			if got := rep.Problems; (tc.problems < 0) != (got == nil) || (got != nil && len(*got) != tc.problems) {
				t.Errorf("problems %v, want %d", got, tc.problems)
			}
			switch l := rep.Envelope.Ledger; {
			case tc.ledger == "" && l != nil:
				t.Errorf("ledger %+v without --check", l)
			case tc.ledger != "" && (l == nil || l.Path != filepath.Base(tc.ledger) || l.SHA256 != fileSHA256(t, tc.ledger)):
				t.Errorf("ledger %+v", l)
			}
			if r := rep.Envelope.RegistrySHA256; slices.Contains(tc.args, "--registry") != (r != nil) || (r != nil && *r != fileSHA256(t, registry)) {
				t.Errorf("registry %v", r)
			}
		})
	}
}

func TestLedgerErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"ledger_version": 1, "types": [], "extra": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no directory", args: []string{"--schema-files", ledgerSchema}, want: "usage:"},
		{name: "two directories", args: []string{ledgerModule, ledgerModule, "--schema-files", ledgerSchema}, want: "usage:"},
		{name: "no schema", args: []string{ledgerModule}, want: "--schema-files"},
		{name: "malformed ledger", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--check", bad}, want: "unknown key"},
		{name: "missing ledger", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--check", filepath.Join(dir, "none.json")}, want: "not a regular file"},
		{name: "missing registry", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--registry", filepath.Join(dir, "none.md")}, want: "registry:"},
		{name: "empty check", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--check", ""}, want: "--check needs a path"},
		{name: "empty check with =", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--check="}, want: "--check needs a path"},
		{name: "empty registry", args: []string{ledgerModule, "--schema-files", ledgerSchema, "--registry", ""}, want: "--registry needs a path"},
		{name: "empty schema file", args: []string{ledgerModule, "--schema-files", ""}, want: "empty path"},
		{name: "unknown flag", args: []string{ledgerModule, "--schema", ledgerSchema}, want: "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(append([]string{"ledger"}, tc.args...), &out, &errb); code != exitError || out.Len() != 0 || !strings.Contains(errb.String(), tc.want) {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}
