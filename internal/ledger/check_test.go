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

func TestCheck(t *testing.T) {
	t.Parallel()
	inv, err := Build(testModule, []string{testSchema}, facts.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	valid := testdataLedger(t, "valid.json")
	logging := `{"name": "logging.filter.suffix", "disposition": "input", "variable": "logging"},
        {"name": "logging.prefix", "disposition": "input", "variable": "logging"},
        {"name": "logging.target", "disposition": "input", "variable": "logging"},`
	if !strings.Contains(valid, logging) {
		t.Fatal("valid.json has changed; update the test")
	}
	tags := `{"name": "tags", "disposition": "input", "variable": "tags"}`
	edit := func(old, repl string) string { return strings.Replace(valid, old, repl, 1) }
	bucket := func(field, problem string) string { return "resource aws_example_bucket " + field + ": " + problem }
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{name: "valid", doc: valid, want: []string{}},
		{name: "unmapped", doc: testdataLedger(t, "unmapped.json"), want: []string{bucket("password", ProblemUnmapped)}},
		{name: "duplicate", doc: testdataLedger(t, "duplicate.json"), want: []string{bucket("tags", ProblemDuplicate)}},
		{
			name: "half-exposed nested block",
			doc:  testdataLedger(t, "half-exposed.json"),
			want: []string{bucket("logging.filter.suffix", ProblemUnmapped)},
		},
		{
			name: "input on a whole block",
			doc:  edit(logging, `{"name": "logging", "disposition": "input", "variable": "logging"},`),
			want: []string{
				bucket("logging", ProblemContainer),
				bucket("logging.filter.suffix", ProblemUnmapped), bucket("logging.prefix", ProblemUnmapped), bucket("logging.target", ProblemUnmapped),
			},
		},
		{
			name: "sensitive omission on a whole block",
			doc:  edit(logging, `{"name": "logging", "disposition": "sensitive_omission", "reason": "r"},`),
			want: []string{
				bucket("logging", ProblemContainer),
				bucket("logging.filter.suffix", ProblemUnmapped), bucket("logging.prefix", ProblemUnmapped), bucket("logging.target", ProblemUnmapped),
			},
		},
		{
			name: "not_applicable on a whole block covers its fields",
			doc:  edit(logging, `{"name": "logging", "disposition": "not_applicable", "reason": "no logging"},`),
			want: []string{},
		},
		{
			name: "omission on an inner block covers its fields",
			doc: edit(`{"name": "logging.filter.suffix", "disposition": "input", "variable": "logging"},`,
				`{"name": "logging.filter", "disposition": "intentional_omission", "reason": "not yet requested"},`),
			want: []string{},
		},
		{
			name: "omitted block and a field inside it",
			doc:  edit(logging, `{"name": "logging", "disposition": "not_applicable", "reason": "r"}, {"name": "logging.prefix", "disposition": "input", "variable": "logging"},`),
			want: []string{bucket("logging.prefix", ProblemDuplicate)},
		},
		{
			name: "omitted block and an omitted block inside it",
			doc:  edit(logging, `{"name": "logging", "disposition": "not_applicable", "reason": "r"}, {"name": "logging.filter", "disposition": "not_applicable", "reason": "r"},`),
			want: []string{bucket("logging.filter", ProblemDuplicate), bucket("logging.filter.suffix", ProblemDuplicate)},
		},
		{
			name: "nested attribute fields mapped one by one",
			doc: edit(`{"name": "rules", "disposition": "intentional_omission", "reason": "not yet requested"},`,
				`{"name": "rules.expiration_days", "disposition": "input", "variable": "tags"}, {"name": "rules.prefix", "disposition": "not_applicable", "reason": "r"},`),
			want: []string{},
		},
		{
			name: "input on a computed-only field",
			doc:  edit(`{"name": "arn", "disposition": "output", "output": "bucket_arn"}`, `{"name": "arn", "disposition": "input", "variable": "name"}`),
			want: []string{bucket("arn", ProblemInputComputed)},
		},
		{
			name: "input on an optional computed field",
			doc:  edit(`{"name": "id", "disposition": "output", "output": "bucket_id"}`, `{"name": "id", "disposition": "input", "variable": "name"}`),
			want: []string{},
		},
		{
			name: "missing variable",
			doc:  edit(tags, `{"name": "tags", "disposition": "input", "variable": "bucket_tags"}`),
			want: []string{bucket("tags", ProblemMissingVariable)},
		},
		{
			name: "missing output",
			doc:  edit(`"output": "region"`, `"output": "region_name"`),
			want: []string{"data aws_example_region name: " + ProblemMissingOutput},
		},
		{
			name: "unknown field",
			doc:  edit(tags, tags+`, {"name": "logging.acl", "disposition": "input", "variable": "tags"}, {"name": "logging.acl", "disposition": "input", "variable": "tags"}`),
			want: []string{bucket("logging.acl", ProblemUnknownField)},
		},
		{
			name: "unknown type",
			doc:  edit(`"types": [`, `"types": [{"mode": "resource", "type": "aws_example_region", "fields": []}, {"mode": "resource", "type": "aws_example_region", "fields": []},`),
			want: []string{"resource aws_example_region : " + ProblemDuplicateType, "resource aws_example_region : " + ProblemUnusedType},
		},
		{
			name: "missing type",
			doc:  `{"ledger_version": 1, "types": [{"mode": "data", "type": "aws_example_region", "fields": [{"name": "id", "disposition": "not_applicable", "reason": "r"}, {"name": "name", "disposition": "output", "output": "region"}]}]}`,
			want: []string{
				bucket("arn", ProblemUnmapped), bucket("encryption.algorithm", ProblemUnmapped), bucket("id", ProblemUnmapped),
				bucket("legacy_acl", ProblemUnmapped), bucket("logging.filter.suffix", ProblemUnmapped), bucket("logging.prefix", ProblemUnmapped),
				bucket("logging.target", ProblemUnmapped), bucket("name", ProblemUnmapped), bucket("password", ProblemUnmapped),
				bucket("password_wo", ProblemUnmapped), bucket("rules.expiration_days", ProblemUnmapped), bucket("rules.prefix", ProblemUnmapped),
				bucket("tags", ProblemUnmapped), bucket("website.index_document", ProblemUnmapped),
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
