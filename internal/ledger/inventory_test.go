package ledger

import (
	"fmt"
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
	// One entry per type, though the module declares aws_example_bucket
	// twice; nested fields by path, depth first.
	want := []string{
		"data aws_example_region id attribute string optional computed",
		"data aws_example_region name attribute string computed",
		"resource aws_example_bucket arn attribute string computed",
		"resource aws_example_bucket encryption block block required nesting=list min=1 max=1",
		"resource aws_example_bucket encryption.algorithm attribute string required",
		"resource aws_example_bucket id attribute string optional computed",
		"resource aws_example_bucket legacy_acl attribute string optional deprecated",
		"resource aws_example_bucket logging block block optional nesting=list min=0 max=1",
		"resource aws_example_bucket logging.filter block block optional nesting=set min=0 max=0",
		"resource aws_example_bucket logging.filter.suffix attribute string optional",
		"resource aws_example_bucket logging.prefix attribute string optional",
		"resource aws_example_bucket logging.target attribute string required",
		"resource aws_example_bucket name attribute string required",
		"resource aws_example_bucket password attribute string optional sensitive",
		"resource aws_example_bucket password_wo attribute string optional write_only",
		"resource aws_example_bucket rules attribute nested optional nesting=set min=0 max=0",
		"resource aws_example_bucket rules.expiration_days attribute number optional",
		"resource aws_example_bucket rules.prefix attribute string optional",
		"resource aws_example_bucket tags attribute map of string optional",
		"resource aws_example_bucket website block block optional deprecated nesting=single min=0 max=0",
		"resource aws_example_bucket website.index_document attribute string optional",
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
	if f.MinItems != nil && f.MaxItems != nil {
		s += fmt.Sprintf(" min=%d max=%d", *f.MinItems, *f.MaxItems)
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
			name:    "ephemeral block names its file and line",
			files:   map[string]string{"main.tf": "\nephemeral \"aws_example_secret\" \"x\" {}\n"},
			schemas: []string{testSchema},
			want:    "main.tf:2: ephemeral resources are not inventoried",
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

// TestBuildMissingRoot: a module directory that cannot be read stops the
// run. The root is read as a tree, so there is no area issue to report.
func TestBuildMissingRoot(t *testing.T) {
	t.Parallel()
	if _, err := Build(filepath.Join(t.TempDir(), "none"), []string{testSchema}, facts.DefaultLimits); err == nil ||
		!strings.Contains(err.Error(), "module tree:") {
		t.Errorf("error %v", err)
	}
}

// TestBuildMixedCaseSource: a required_providers source in mixed case finds
// the schema file's lower-case key.
func TestBuildMixedCaseSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tf := "terraform {\n  required_providers {\n    aws = { source = \"HashiCorp/AWS\" }\n  }\n}\nresource \"aws_example_bucket\" \"x\" {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(tf), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err := Build(dir, []string{testSchema}, facts.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Types) != 1 || inv.Types[0].Provider != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("types %+v", inv.Types)
	}
}

// TestBuildDepthCap: a schema nested deeper than maxDepth stops the run.
func TestBuildDepthCap(t *testing.T) {
	t.Parallel()
	block := `{"attributes": {"leaf": {"type": "string", "optional": true}}}`
	for range maxDepth + 2 {
		block = `{"block_types": {"b": {"nesting_mode": "single", "block": ` + block + `}}}`
	}
	doc := `{"format_version": "1.0", "provider_schemas": {"registry.terraform.io/hashicorp/aws": {"resource_schemas": {"aws_deep": {"version": 0, "block": ` + block + `}}}}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deep.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`resource "aws_deep" "x" {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(dir, []string{filepath.Join(dir, "deep.json")}, facts.DefaultLimits); err == nil || !strings.Contains(err.Error(), "deeper than") {
		t.Errorf("error %v", err)
	}
}
