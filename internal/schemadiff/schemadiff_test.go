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

func runTestdata(t *testing.T) *Report {
	t.Helper()
	r, err := Run(Options{
		From:      filepath.Join(testdata, "from.json"),
		To:        filepath.Join(testdata, "to.json"),
		TypesFrom: filepath.Join(testdata, "module"),
		Limits:    facts.DefaultLimits,
		Tool:      Tool{Name: "mtf-facts", Version: "v0.3.0", BinarySHA256: strings.Repeat("2", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// asJSON decodes v's JSON form, so values compare as the report writes them.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRunMatchesExpected(t *testing.T) {
	t.Parallel()
	r := runTestdata(t)
	want := readJSON(t, filepath.Join(testdata, "expected.json"))
	if got := asJSON(t, r.Types); !reflect.DeepEqual(got, want["types"]) {
		t.Errorf("types differ from expected.json; got:\n%v", got)
	}
	got := asJSON(t, r.Changes).([]any)
	wantChanges := want["changes"].([]any)
	for i := range max(len(got), len(wantChanges)) {
		switch {
		case i >= len(got):
			t.Errorf("missing row %v", wantChanges[i])
		case i >= len(wantChanges):
			t.Errorf("extra row %v", got[i])
		case !reflect.DeepEqual(got[i], wantChanges[i]):
			t.Errorf("row %d:\n got %v\nwant %v", i, got[i], wantChanges[i])
		}
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

// The schema example is the real output, so the schema checks on the example
// (tests/schemas-test.sh and the validator in internal/facts) check it.
func TestValidExampleIsRealOutput(t *testing.T) {
	t.Parallel()
	got := asJSON(t, runTestdata(t))
	if want := readJSON(t, "../../schema/examples/schema-diff.valid.json"); !reflect.DeepEqual(got, any(want)) {
		out, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("schema/examples/schema-diff.valid.json is not the real output; it is:\n%s", out)
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

// writeSchema writes from.json of testdata less the providers drop names.
func writeSchema(t *testing.T, dir, name string, drop ...string) string {
	t.Helper()
	doc := readJSON(t, filepath.Join(testdata, "from.json"))
	ps := doc["provider_schemas"].(map[string]any)
	for _, d := range drop {
		delete(ps, d)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, src string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return filepath.Dir(p)
	}
	override := write("override/override.tf", `resource "aws_x" "y" {}`)
	ephemeral := write("ephemeral/main.tf", `ephemeral "aws_secret" "x" {}`)
	const example = "registry.terraform.io/acme/example"
	noExample := writeSchema(t, dir, "no-example.json", example)
	from, to, module := filepath.Join(testdata, "from.json"), filepath.Join(testdata, "to.json"), filepath.Join(testdata, "module")
	for _, tc := range []struct {
		name, from, to, types, want string
	}{
		{"missing from", filepath.Join(dir, "none.json"), to, module, "--from: schema file: open none.json: "},
		{"malformed to", from, filepath.Join(module, "main.tf"), module, "--to: schema file main.tf: malformed"},
		{"missing tree", from, to, filepath.Join(dir, "none"), "--types-from: "},
		{"override file", from, to, override, "override file"},
		{"ephemeral block", from, to, ephemeral, "main.tf:1: ephemeral resources are not compared"},
		{"provider missing from from", noExample, to, module,
			"provider " + example + ", which resource example_widget uses, is not in no-example.json"},
		{"provider missing from to", from, noExample, module,
			"provider " + example + ", which resource example_widget uses, is not in no-example.json"},
		{"provider missing from both", noExample, noExample, module, "is not in no-example.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Run(Options{From: tc.from, To: tc.to, TypesFrom: tc.types, Limits: facts.DefaultLimits})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), dir) {
				t.Errorf("error names a host path: %v", err)
			}
		})
	}
}

func TestDiffEdges(t *testing.T) {
	t.Parallel()
	ty := Type{Mode: "resource", Type: "x_y", ProviderSource: "registry.terraform.io/hashicorp/x"}
	opt := &tfjson.SchemaAttribute{Optional: true}
	req := &tfjson.SchemaAttribute{Required: true}
	attrs := func(a *tfjson.SchemaAttribute) *tfjson.SchemaBlock {
		return &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{"a": a}}
	}
	nested := func(minItems uint64) *tfjson.SchemaBlock {
		return &tfjson.SchemaBlock{NestedBlocks: map[string]*tfjson.SchemaBlockType{
			"b": {NestingMode: tfjson.SchemaNestingModeList, MinItems: minItems, Block: attrs(opt)},
		}}
	}
	nestedAttr := func(mode tfjson.SchemaNestingMode) *tfjson.SchemaBlock {
		return attrs(&tfjson.SchemaAttribute{Optional: true, AttributeNestedType: &tfjson.SchemaNestedAttributeType{
			NestingMode: mode,
			Attributes:  map[string]*tfjson.SchemaAttribute{"c": opt},
		}})
	}
	maxItems := func(n uint64) *tfjson.SchemaBlock {
		return &tfjson.SchemaBlock{NestedBlocks: map[string]*tfjson.SchemaBlockType{
			"b": {NestingMode: tfjson.SchemaNestingModeList, MaxItems: n, Block: attrs(opt)},
		}}
	}
	for _, tc := range []struct {
		name     string
		from, to *tfjson.SchemaBlock
		want     []string // path:element:change
	}{
		{"unchanged", attrs(opt), attrs(opt), nil},
		{"added required argument", &tfjson.SchemaBlock{}, attrs(req),
			[]string{"a:argument:added", "a:argument:newly_required"}},
		{"required to optional is no row", attrs(req), attrs(opt), nil},
		{"required to computed-only", attrs(req), attrs(&tfjson.SchemaAttribute{Computed: true}),
			[]string{"a:attribute:no_longer_settable"}},
		{"optional and computed stays settable", attrs(opt), attrs(&tfjson.SchemaAttribute{Optional: true, Computed: true}), nil},
		{"existing block min_items 0 to 1", nested(0), nested(1),
			[]string{"b:block:newly_required", "b:block:nesting_changed"}},
		{"existing block min_items 1 to 0", nested(1), nested(0), []string{"b:block:nesting_changed"}},
		{"nil attribute and block entries are absent", &tfjson.SchemaBlock{
			Attributes:   map[string]*tfjson.SchemaAttribute{"a": nil},
			NestedBlocks: map[string]*tfjson.SchemaBlockType{"b": {}},
		}, &tfjson.SchemaBlock{}, nil},
		{"added block contents not listed", &tfjson.SchemaBlock{}, nested(0), []string{"b:block:added"}},
		{"already deprecated is no row", &tfjson.SchemaBlock{Deprecated: true}, &tfjson.SchemaBlock{Deprecated: true}, nil},
		{"type in neither file", nil, nil, []string{":type:absent"}},
		{"nested list to set, same attributes", nestedAttr(tfjson.SchemaNestingModeList), nestedAttr(tfjson.SchemaNestingModeSet),
			[]string{"a:argument:type_changed"}},
		{"block max_items 1 to 0, same nesting mode", maxItems(1), maxItems(0), []string{"b:block:nesting_changed"}},
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
