package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
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
		{name: "empty types", doc: `{"ledger_version": 1, "types": []}`, ok: true},
		{name: "no types", doc: `{"ledger_version": 1}`},
		{name: "version 2", doc: `{"ledger_version": 2, "types": []}`},
		{name: "unknown key", doc: `{"ledger_version": 1, "types": [], "x": 1}`},
		{name: "two values", doc: `{"ledger_version": 1, "types": []} {}`},
		{name: "bad mode", doc: `{"ledger_version": 1, "types": [{"mode": "module", "type": "aws_x", "fields": []}]}`},
		{name: "no fields", doc: `{"ledger_version": 1, "types": [{"mode": "data", "type": "aws_x"}]}`},
		{name: "unknown disposition", doc: field(`{"name": "a", "disposition": "ignored", "reason": "r"}`)},
		{name: "input without variable", doc: field(`{"name": "a", "disposition": "input"}`)},
		{name: "input with reason", doc: field(`{"name": "a", "disposition": "input", "variable": "a", "reason": "r"}`)},
		{name: "output with variable", doc: field(`{"name": "a", "disposition": "output", "output": "a", "variable": "a"}`)},
		{name: "omission without reason", doc: field(`{"name": "a", "disposition": "intentional_omission"}`)},
		{name: "omission with output", doc: field(`{"name": "a", "disposition": "sensitive_omission", "reason": "r", "output": "a"}`)},
		{name: "bad field name", doc: field(`{"name": "a.b", "disposition": "input", "variable": "a"}`)},
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
// schema's valid examples parse and its invalid ones do not.
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

func TestCheck(t *testing.T) {
	t.Parallel()
	inv, err := Build(testModule, []string{testSchema}, facts.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile("../../testdata/ledger/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	bucket := `{"name": "tags", "disposition": "input", "variable": "tags"}`
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{name: "valid", doc: string(valid), want: []string{}},
		{name: "unmapped", doc: testdataLedger(t, "unmapped.json"), want: []string{"resource aws_example_bucket password: " + ProblemUnmapped}},
		{name: "duplicate", doc: testdataLedger(t, "duplicate.json"), want: []string{"resource aws_example_bucket tags: " + ProblemDuplicate}},
		{
			name: "missing variable",
			doc:  strings.Replace(string(valid), bucket, `{"name": "tags", "disposition": "input", "variable": "bucket_tags"}`, 1),
			want: []string{"resource aws_example_bucket tags: " + ProblemMissingVariable},
		},
		{
			name: "missing output",
			doc:  strings.Replace(string(valid), `"output": "region"`, `"output": "region_name"`, 1),
			want: []string{"data aws_example_region name: " + ProblemMissingOutput},
		},
		{
			name: "unknown field",
			doc:  strings.Replace(string(valid), bucket, bucket+`, {"name": "acl", "disposition": "input", "variable": "tags"}, {"name": "acl", "disposition": "input", "variable": "tags"}`, 1),
			want: []string{"resource aws_example_bucket acl: " + ProblemUnknownField},
		},
		{
			name: "unknown type",
			doc:  strings.Replace(string(valid), `"types": [`, `"types": [{"mode": "resource", "type": "aws_example_region", "fields": []}, {"mode": "resource", "type": "aws_example_region", "fields": []},`, 1),
			want: []string{"resource aws_example_region : " + ProblemDuplicateType, "resource aws_example_region : " + ProblemUnusedType},
		},
		{
			name: "missing type",
			doc:  `{"ledger_version": 1, "types": [{"mode": "data", "type": "aws_example_region", "fields": [{"name": "id", "disposition": "not_applicable", "reason": "r"}, {"name": "name", "disposition": "output", "output": "region"}]}]}`,
			want: []string{
				"resource aws_example_bucket arn: " + ProblemUnmapped, "resource aws_example_bucket id: " + ProblemUnmapped,
				"resource aws_example_bucket legacy_acl: " + ProblemUnmapped, "resource aws_example_bucket logging: " + ProblemUnmapped,
				"resource aws_example_bucket name: " + ProblemUnmapped, "resource aws_example_bucket password: " + ProblemUnmapped,
				"resource aws_example_bucket rules: " + ProblemUnmapped, "resource aws_example_bucket tags: " + ProblemUnmapped,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, err := Parse([]byte(tc.doc))
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, p := range Check(inv, l) {
				got = append(got, fmt.Sprintf("%s %s %s: %s", p.Mode, p.Type, p.Field, p.Problem))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func testdataLedger(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("../../testdata/ledger", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
	}{{p, 4}, {filepath.Join(dir, "link.json"), 5}, {dir, 5}, {filepath.Join(dir, "none"), 5}} {
		if _, err := ReadFile(tc.path, tc.limit); err == nil {
			t.Errorf("%s with limit %d: no error", tc.path, tc.limit)
		}
	}
}
