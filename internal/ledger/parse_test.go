package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	field := func(f string) string {
		return `{"ledger_version": 1, "types": [{"mode": "resource", "type": "aws_x", "fields": [` + f + `]}]}`
	}
	tests := []struct {
		name string
		doc  string
		ok   bool
	}{
		{name: "input", doc: field(`{"name": "a", "disposition": "input", "variable": "a"}`), ok: true},
		{name: "output", doc: field(`{"name": "a", "disposition": "output", "output": "a"}`), ok: true},
		{name: "omission", doc: field(`{"name": "a", "disposition": "not_applicable", "reason": "r"}`), ok: true},
		{name: "nested path", doc: field(`{"name": "a.b_c.d", "disposition": "input", "variable": "a"}`), ok: true},
		{name: "empty types", doc: `{"ledger_version": 1, "types": []}`, ok: true},
		{name: "no types", doc: `{"ledger_version": 1}`},
		{name: "null types", doc: `{"ledger_version": 1, "types": null}`},
		{name: "version 2", doc: `{"ledger_version": 2, "types": []}`},
		{name: "unknown key", doc: `{"ledger_version": 1, "types": [], "x": 1}`},
		{name: "two values", doc: `{"ledger_version": 1, "types": []} {}`},
		{name: "not an object", doc: `[]`},
		{name: "bad mode", doc: `{"ledger_version": 1, "types": [{"mode": "module", "type": "aws_x", "fields": []}]}`},
		{name: "no fields", doc: `{"ledger_version": 1, "types": [{"mode": "data", "type": "aws_x"}]}`},
		{name: "unknown disposition", doc: field(`{"name": "a", "disposition": "ignored", "reason": "r"}`)},
		{name: "input without variable", doc: field(`{"name": "a", "disposition": "input"}`)},
		{name: "input with reason", doc: field(`{"name": "a", "disposition": "input", "variable": "a", "reason": "r"}`)},
		{name: "input with empty reason", doc: field(`{"name": "a", "disposition": "input", "variable": "a", "reason": ""}`)},
		{name: "input with dotted variable", doc: field(`{"name": "a", "disposition": "input", "variable": "a.b"}`)},
		{name: "output with variable", doc: field(`{"name": "a", "disposition": "output", "output": "a", "variable": "a"}`)},
		{name: "omission without reason", doc: field(`{"name": "a", "disposition": "intentional_omission"}`)},
		{name: "omission with empty reason", doc: field(`{"name": "a", "disposition": "intentional_omission", "reason": ""}`)},
		{name: "omission with output", doc: field(`{"name": "a", "disposition": "sensitive_omission", "reason": "r", "output": "a"}`)},
		{name: "null reason", doc: field(`{"name": "a", "disposition": "not_applicable", "reason": null}`)},
		{name: "number name", doc: field(`{"name": 1, "disposition": "input", "variable": "a"}`)},
		{name: "path with empty segment", doc: field(`{"name": "a..b", "disposition": "input", "variable": "a"}`)},
		{name: "path with leading dot", doc: field(`{"name": ".a", "disposition": "input", "variable": "a"}`)},
		{name: "path with trailing dot", doc: field(`{"name": "a.", "disposition": "input", "variable": "a"}`)},
		{name: "path segment starting with a digit", doc: field(`{"name": "a.1b", "disposition": "input", "variable": "a"}`)},
		{name: "path with a space", doc: field(`{"name": "a b", "disposition": "input", "variable": "a"}`)},
		{name: "empty path", doc: field(`{"name": "", "disposition": "input", "variable": "a"}`)},
		{name: "path with dashes and underscores", doc: field(`{"name": "_a-b.c_d", "disposition": "input", "variable": "a"}`), ok: true},
		// encoding/json folds case and keeps the last duplicate; the schema's
		// additionalProperties false refuses the first, and a ledger must not
		// say two things of one field either.
		{name: "case-mangled top key", doc: `{"Ledger_Version": 1, "types": []}`},
		{name: "case-mangled field key", doc: field(`{"Name": "a", "disposition": "input", "variable": "a"}`)},
		{name: "case-mangled disposition key", doc: field(`{"name": "a", "Disposition": "input", "variable": "a"}`)},
		{name: "case-mangled type key", doc: `{"ledger_version": 1, "types": [{"Mode": "data", "type": "aws_x", "fields": []}]}`},
		{name: "duplicate top key", doc: `{"ledger_version": 1, "types": [], "types": []}`},
		{name: "duplicate field key", doc: field(`{"name": "a", "name": "b", "disposition": "input", "variable": "a"}`)},
		{name: "duplicate key in type", doc: `{"ledger_version": 1, "types": [{"mode": "data", "mode": "resource", "type": "aws_x", "fields": []}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Parse([]byte(tc.doc)); (err == nil) != tc.ok {
				t.Errorf("error %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// TestSchemaExamples keeps Parse and schema/ledger.schema.json in step: the
// schema's valid examples parse and its invalid ones do not. Duplicate keys
// are left to TestParse: JSON Schema validators read a parsed document, in
// which a duplicate key is already gone.
func TestSchemaExamples(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../schema/examples/ledger.*.json")
	if err != nil || len(files) < 2 {
		t.Fatalf("examples %v %v", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		valid := strings.Contains(filepath.Base(f), ".valid.")
		if _, err := Parse(data); (err == nil) != valid {
			t.Errorf("%s: error %v", f, err)
		}
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "l.json")
	if err := os.WriteFile(p, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(p, 5); err != nil || string(data) != "12345" {
		t.Errorf("%q %v", data, err)
	}
	for _, tc := range []struct {
		path  string
		limit int64
	}{{p, 4}, {filepath.Join(dir, "link.json"), 5}, {dir, 5}, {filepath.Join(dir, "none"), 5}, {"", 5}} {
		if _, err := ReadFile(tc.path, tc.limit); err == nil {
			t.Errorf("%q with limit %d: no error", tc.path, tc.limit)
		}
	}
}
