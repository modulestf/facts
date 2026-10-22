package schemadiff

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/modulestf/facts/internal/facts"
)

const testdata = "../../testdata/schema-diff"

func TestRunMatchesExpected(t *testing.T) {
	t.Parallel()
	r, err := Run(Options{
		From:      filepath.Join(testdata, "from.json"),
		To:        filepath.Join(testdata, "to.json"),
		TypesFrom: filepath.Join(testdata, "module"),
		Limits:    facts.DefaultLimits,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(testdata, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Types   []Type  `json:"types"`
		Changes []Entry `json:"changes"`
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Types, want.Types) {
		t.Errorf("types:\n got %+v\nwant %+v", r.Types, want.Types)
	}
	if !reflect.DeepEqual(r.Changes, want.Changes) {
		got, _ := json.MarshalIndent(r.Changes, "", "  ")
		t.Errorf("changes differ from expected.json; got:\n%s", got)
	}
	for _, f := range []struct {
		got  File
		name string
	}{{r.From, "from.json"}, {r.To, "to.json"}} {
		if f.got != (File{Path: f.name, SHA256: fileSHA256(t, filepath.Join(testdata, f.name))}) {
			t.Errorf("%s recorded as %+v", f.name, f.got)
		}
	}
}

func fileSHA256(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "override.tf"), []byte(`resource "aws_x" "y" {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	from, to, module := filepath.Join(testdata, "from.json"), filepath.Join(testdata, "to.json"), filepath.Join(testdata, "module")
	for _, tc := range []struct {
		name, from, to, types, want string
	}{
		{"missing from", filepath.Join(dir, "none.json"), to, module, "--from:"},
		{"malformed to", from, filepath.Join(testdata, "module", "main.tf"), module, "--to:"},
		{"missing tree", from, to, filepath.Join(dir, "none"), "--types-from:"},
		{"override file", from, to, dir, "override file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Run(Options{From: tc.from, To: tc.to, TypesFrom: tc.types, Limits: facts.DefaultLimits})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDiffEdges(t *testing.T) {
	t.Parallel()
	ty := Type{Mode: "resource", Type: "x_y", ProviderSource: "registry.terraform.io/hashicorp/x"}
	opt := &tfjson.SchemaAttribute{Optional: true}
	req := &tfjson.SchemaAttribute{Required: true}
	for _, tc := range []struct {
		name     string
		from, to *tfjson.SchemaBlock
		want     []string // path:element:change
	}{
		{"unchanged", &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": opt}},
			&tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": opt}}, nil},
		{"added required argument", &tfjson.SchemaBlock{},
			&tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": req}},
			[]string{"a:argument:added", "a:argument:newly_required"}},
		{"required to optional is no row", &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": req}},
			&tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": opt}}, nil},
		{"nil attribute and block entries are absent", &tfjson.SchemaBlock{
			Attributes:   map[string]*tfjson.SchemaAttribute{"a": nil},
			NestedBlocks: map[string]*tfjson.SchemaBlockType{"b": {}},
		}, &tfjson.SchemaBlock{}, nil},
		{"added block contents not listed", &tfjson.SchemaBlock{},
			&tfjson.SchemaBlock{NestedBlocks: map[string]*tfjson.SchemaBlockType{
				"b": {Block: &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"c": req}}},
			}}, []string{"b:block:added"}},
		{"already deprecated is no row", &tfjson.SchemaBlock{Deprecated: true}, &tfjson.SchemaBlock{Deprecated: true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := []string{}
			for _, e := range Diff(ty, tc.from, tc.to) {
				got = append(got, strings.Join(e.Path, ".")+":"+e.Element+":"+e.Change)
			}
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProviderKey(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"aws":                                 "registry.terraform.io/hashicorp/aws",
		"Acme/Example":                        "registry.terraform.io/acme/example",
		"example.com/acme/example":            "example.com/acme/example",
		"registry.terraform.io/hashicorp/aws": "registry.terraform.io/hashicorp/aws",
	} {
		if got := providerKey(in); got != want {
			t.Errorf("providerKey(%q) = %q, want %q", in, got, want)
		}
	}
}
