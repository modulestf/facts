package ledger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

const (
	testModule = "../../testdata/ledger/module"
	testSchema = "../../testdata/ledger/aws.json"
)

func TestBuild(t *testing.T) {
	t.Parallel()
	inv, err := Build(testModule, []string{testSchema}, facts.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ty := range inv.Types {
		if ty.Provider != "registry.terraform.io/hashicorp/aws" {
			t.Errorf("%s: provider %q", ty.Type, ty.Provider)
		}
		for _, f := range ty.Fields {
			got = append(got, ty.Mode+" "+ty.Type+" "+fieldString(f))
		}
	}
	// One entry per type, though the module declares aws_example_bucket twice.
	want := []string{
		"data aws_example_region id attribute string optional computed",
		"data aws_example_region name attribute string computed",
		"resource aws_example_bucket arn attribute string computed",
		"resource aws_example_bucket id attribute string optional computed",
		"resource aws_example_bucket legacy_acl attribute string optional deprecated",
		"resource aws_example_bucket logging block block optional nesting=list",
		"resource aws_example_bucket name attribute string required",
		"resource aws_example_bucket password attribute string optional sensitive",
		"resource aws_example_bucket rules attribute nested optional nesting=set",
		"resource aws_example_bucket tags attribute map of string optional",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(inv.SchemaFiles) != 1 || inv.SchemaFiles[0].Path != "aws.json" || len(inv.SchemaFiles[0].SHA256) != 64 {
		t.Errorf("schema files %+v", inv.SchemaFiles)
	}
	if !inv.variables["logging"] || !inv.outputs["region"] || inv.variables["region"] {
		t.Errorf("variables %v outputs %v", inv.variables, inv.outputs)
	}
}

func fieldString(f Field) string {
	s := f.Name + " " + f.Kind + " " + f.Type
	for _, flag := range []struct {
		on   bool
		name string
	}{{f.Required, "required"}, {f.Optional, "optional"}, {f.Computed, "computed"}, {f.Deprecated, "deprecated"}, {f.Sensitive, "sensitive"}, {f.WriteOnly, "write_only"}} {
		if flag.on {
			s += " " + flag.name
		}
	}
	if f.Nesting != "" {
		s += " nesting=" + f.Nesting
	}
	return s
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		files   map[string]string
		schemas []string
		want    string
	}{
		{name: "no schema file", files: map[string]string{"main.tf": `resource "aws_example_bucket" "x" {}`}, want: "at least one --schema-files"},
		{name: "type not in schema", files: map[string]string{"main.tf": `resource "aws_other" "x" {}`}, schemas: []string{testSchema}, want: "has no such type"},
		{name: "data source looked up as data", files: map[string]string{"main.tf": `data "aws_example_bucket" "x" {}`}, schemas: []string{testSchema}, want: "has no such type"},
		{name: "provider not in schema", files: map[string]string{"main.tf": `resource "random_id" "x" {}`}, schemas: []string{testSchema}, want: "no schema file has provider registry.terraform.io/hashicorp/random"},
		{
			name:    "required_providers source is used",
			files:   map[string]string{"main.tf": "terraform {\n  required_providers {\n    aws = { source = \"example/aws\" }\n  }\n}\nresource \"aws_example_bucket\" \"x\" {}\n"},
			schemas: []string{testSchema},
			want:    "no schema file has provider registry.terraform.io/example/aws",
		},
		{
			name:    "provider argument names the local name",
			files:   map[string]string{"main.tf": `resource "aws_example_bucket" "x" { provider = google.west }`},
			schemas: []string{testSchema},
			want:    "registry.terraform.io/hashicorp/google",
		},
		{name: "parse error", files: map[string]string{"main.tf": `resource "aws_example_bucket" {`}, schemas: []string{testSchema}, want: "module tree:"},
		{name: "json file", files: map[string]string{"main.tf.json": `{}`}, schemas: []string{testSchema}, want: "module tree:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Build(dir, tc.schemas, facts.DefaultLimits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}
