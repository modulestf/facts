package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
)

// rawWithUnused is rawSchema with a second resource type no test looks up,
// whose body is %s.
const rawWithUnused = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/hashicorp/aws": {
      %s
      "resource_schemas": {
        "aws_demo_widget": {"version": 0, "block": {"attributes": {"name": {"type": "string", "required": true}}}},
        "aws_unused_thing": %s
      },
      "data_source_schemas": {"aws_demo_lookup": {"version": 0, "block": {"attributes": {"id": {"type": "string", "computed": true}}}}}
    }
  }
}`

func unusedDoc(provider, typ string) string {
	return strings.Replace(strings.Replace(rawWithUnused, "%s", provider, 1), "%s", typ, 1)
}

// TestRawUnusedTypeRefused: a type the run never looks up is still checked
// at load, so a file Load refused before types were decoded lazily is
// refused now, by Load and by LoadTypes keeping no type.
func TestRawUnusedTypeRefused(t *testing.T) {
	ok := `{"version": 0, "block": {"attributes": {"x": {"type": "string", "optional": true}}}}`
	for name, doc := range map[string]string{
		"attributes not an object": unusedDoc("", `{"block": {"attributes": 5}}`),
		"unknown attribute type":   unusedDoc("", `{"block": {"attributes": {"x": {"type": "nonsense"}}}}`),
		"type not a type":          unusedDoc("", `{"block": {"attributes": {"x": {"type": 5}}}}`),
		"negative min_items":       unusedDoc("", `{"block": {"block_types": {"b": {"nesting_mode": "list", "min_items": -1}}}}`),
		"version not a number":     unusedDoc("", `{"version": "x"}`),
		"required not a bool":      unusedDoc("", `{"block": {"attributes": {"x": {"required": "yes"}}}}`),
		"type not an object":       unusedDoc("", `[]`),
		"provider config shape":    unusedDoc(`"provider": {"block": {"attributes": 5}},`, ok),
		"functions shape":          unusedDoc(`"functions": 5,`, ok),
		"bad first of two":         unusedDoc("", `{"block": {"attributes": 5}}, "aws_unused_thing": `+ok),
	} {
		p := writeFile(t, "aws.json", []byte(doc))
		_, err := Load("", []string{p}, time.Time{})
		if err == nil {
			t.Errorf("%s: Load accepted", name)
		}
		// A bad type is named, with its provider.
		if want := "provider " + aws + ": resource aws_unused_thing: "; !strings.HasPrefix(name, "provider ") &&
			!strings.HasPrefix(name, "functions") && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: error %v does not name %q", name, err, want)
		}
		if _, err := LoadTypes([]string{p}, func(bool, string) bool { return false }, time.Time{}); err == nil {
			t.Errorf("%s: LoadTypes accepted", name)
		}
		var ps tfjson.ProviderSchemas
		if err := ps.UnmarshalJSON([]byte(doc)); err == nil {
			t.Errorf("%s: a whole decode accepts it, so the case tests nothing", name)
		}
	}
	p := writeFile(t, "aws.json", []byte(unusedDoc(`"provider": {"version": 0},`, ok)))
	if _, err := Load("", []string{p}, time.Time{}); err != nil {
		t.Errorf("good file: %v", err)
	}
}

// TestLoadTypes: a kept type answers as Load does, a type not kept is
// schema_unavailable, and a type the file lacks is absent.
func TestLoadTypes(t *testing.T) {
	p := writeFile(t, "aws.json", []byte(unusedDoc("", `null`)))
	all, err := Load("", []string{p}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	some, err := LoadTypes([]string{p}, func(data bool, name string) bool {
		return !data && name == "aws_demo_widget" || name == "aws_unused_thing"
	}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	widget := Query{Source: aws, Type: "aws_demo_widget", Path: []string{"name"}}
	oa, fa := all.Lookup(widget)
	ok, fs := some.Lookup(widget)
	if oa != Present || ok != Present || !reflect.DeepEqual(fa, fs) {
		t.Errorf("kept type: %s %+v, %s %+v", oa, fa, ok, fs)
	}
	lookup := Query{Source: aws, DataSource: true, Type: "aws_demo_lookup"}
	if o, _ := all.Lookup(lookup); o != Present {
		t.Errorf("Load, type not kept elsewhere: %s", o)
	}
	if o, _ := some.Lookup(lookup); o != Unavailable {
		t.Errorf("LoadTypes, type not kept: %s", o)
	}
	for _, s := range []*Set{all, some} {
		if o, _ := s.Lookup(Query{Source: aws, Type: "aws_missing"}); o != AbsentFromSchema {
			t.Errorf("missing type: %s", o)
		}
		// A type written as null has no block, as before.
		if o, _ := s.Lookup(Query{Source: aws, Type: "aws_unused_thing"}); o != AbsentFromSchema {
			t.Errorf("null type: %s", o)
		}
	}
}

// TestLazyLookupMatchesWholeDecode: for every type of the raw schema files
// of the fixtures, a lookup gives the block a whole decode gives, and a
// second lookup the same decoded block.
func TestLazyLookupMatchesWholeDecode(t *testing.T) {
	for _, p := range []string{
		"../../testdata/schema-diff/from.json", "../../testdata/schema-diff/to.json",
		"../../testdata/ledger/aws.json",
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var whole tfjson.ProviderSchemas
		if err := whole.UnmarshalJSON(data); err != nil {
			t.Fatal(err)
		}
		s, err := Load("", []string{p}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for source, ps := range whole.Schemas {
			for i, types := range []map[string]*tfjson.Schema{ps.ResourceSchemas, ps.DataSourceSchemas} {
				for name, ts := range types {
					q := Query{Source: source, DataSource: i == 1, Type: name}
					o, f := s.Lookup(q)
					if o != Present || !reflect.DeepEqual(f.Block, ts.Block) {
						t.Errorf("%s %s: %s", p, name, o)
					}
					if _, again := s.Lookup(q); again.Block != f.Block {
						t.Errorf("%s %s: decoded again", p, name)
					}
					n++
				}
			}
		}
		if n == 0 {
			t.Errorf("%s: no type compared", p)
		}
	}
}

// TestHandoffUnusedTypeRefused: a handoff type no lookup reads is still
// checked in full.
func TestHandoffUnusedTypeRefused(t *testing.T) {
	data, err := os.ReadFile("../../schema/examples/schema-handoff.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	const anchor = `"resource_schemas": {`
	if !bytes.Contains(data, []byte(anchor)) {
		t.Fatal("anchor not found")
	}
	for name, typ := range map[string]string{
		"unknown member":    `{"block": {"attributes": {}, "description": "x"}}`,
		"bad nesting mode":  `{"block": {"block_types": {"b": {"nesting_mode": "bag", "block": {}}}}}`,
		"no block":          `{}`,
		"attribute is null": `{"block": {"attributes": {"a": null}}}`,
		"bad count":         `{"block": {"block_types": {"b": {"nesting_mode": "list", "max_items": 1.5, "block": {}}}}}`,
		"bad first of two":  `{"block": {}, "description": "x"}, "aws_unused_thing": {"block": {}}`,
	} {
		doc := strings.Replace(string(data), anchor, anchor+`"aws_unused_thing": `+typ+`, `, 1)
		_, err := Load(writeFile(t, "h.json", []byte(doc)), nil, time.Time{})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		// A null is refused by the null scan, before any type is read.
		if name != "attribute is null" && (err == nil || !strings.Contains(err.Error(), `type "aws_unused_thing": `)) {
			t.Errorf("%s: error %v does not name the type", name, err)
		}
	}
	doc := strings.Replace(string(data), anchor, anchor+`"aws_unused_thing": {"block": {}}, `, 1)
	if _, err := Load(writeFile(t, "h.json", []byte(doc)), nil, time.Time{}); err != nil {
		t.Errorf("good unused type: %v", err)
	}
}

// nullsByTokens is the token pass nullsOnlyAtOldFloor replaced, kept to
// check the byte scan against it.
func nullsByTokens(doc []byte) error {
	type frame struct {
		array, atValue bool
		key, in        string
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	var st []frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var top *frame
		if len(st) > 0 {
			top = &st[len(st)-1]
		}
		if top != nil && !top.array && !top.atValue {
			if tok == json.Delim('}') {
				st = st[:len(st)-1]
				continue
			}
			top.key, top.atValue = tok.(string), true
			continue
		}
		in := ""
		if top != nil && !top.array {
			in, top.atValue = top.key, false
		}
		switch tok {
		case json.Delim('{'):
			st = append(st, frame{in: in})
		case json.Delim('['):
			st = append(st, frame{array: true, in: in})
		case json.Delim(']'):
			st = st[:len(st)-1]
		case nil:
			if top == nil || top.array || in != "old_floor" || top.in != "versions" {
				return errors.New("null where the schema allows none")
			}
		}
	}
}

var nullCases = []string{
	`null`,
	`{"versions": {"old_floor": null}}`,
	`{"versions": {"old_floor": null, "latest": null}}`,
	`{"versions": {"old\u005ffloor": null}}`,
	`{"vers\u0069ons": {"old_floor": null}}`,
	`{"versions": {"old_floor": [null]}}`,
	`{"versions": [{"old_floor": null}]}`,
	`{"old_floor": null}`,
	`{"x": {"versions": {"old_floor": null}}, "y": [1, -2.5e+3, true, false, "null"]}`,
	`{"a\"versions": {"old_floor": null}}`,
	`{"versions": {"a": "old_floor\\", "old_floor": null}}`,
	`{"versions": {"old_floor": "x"}, "z": null}`,
	`{"versions": {"old_floor": {"a": null}}}`,
	`[{"versions": {"old_floor": null}}, null]`,
	` { "versions" : { "old_floor" : null } } `,
	`{"versions": {"old_floor": 1e5, "k": "v"}}`,
}

func TestNullScanMatchesTokenScan(t *testing.T) {
	data, err := os.ReadFile("../../schema/examples/schema-handoff.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(nullCases, string(data)) {
		got, want := nullsOnlyAtOldFloor([]byte(c)), nullsByTokens([]byte(c))
		if (got == nil) != (want == nil) {
			t.Errorf("%s: byte scan %v, token scan %v", c, got, want)
		}
	}
}

const errNullText = "null where the schema allows none"

// FuzzNullScan holds the byte scan to the token scan on any JSON text, and
// to no panic on any bytes. A number the token pass cannot parse as a
// float64 is out of its scope.
func FuzzNullScan(f *testing.F) {
	for _, c := range nullCases {
		f.Add([]byte(c))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		got := nullsOnlyAtOldFloor(doc)
		if !json.Valid(doc) {
			return
		}
		want := nullsByTokens(doc)
		if want != nil && want.Error() != errNullText {
			return // the token pass also parses numbers; the decode after the scan refuses those
		}
		if (got == nil) != (want == nil) {
			t.Errorf("%q: byte scan %v, token scan %v", doc, got, want)
		}
	})
}

// FuzzRawSchema holds parseRaw to a whole decode into
// tfjson.ProviderSchemas: each accepts what the other accepts, and an
// accepted file has the same providers, types and blocks.
func FuzzRawSchema(f *testing.F) {
	ok := `{"version": 0, "block": {"attributes": {"x": {"type": "string", "optional": true}}}}`
	for _, c := range []string{
		rawSchema, unusedDoc("", ok), unusedDoc("", `null`), unusedDoc(`"provider": {"version": 0},`, ok),
		unusedDoc("", `{"block": {"attributes": {"x": {"type": "nonsense"}}}}`),
		`{"format_version": "1.0", "provider_schemas": {"p": {"resource_schemas": {"a": 5, "a": {}}}}}`,
		`{"format_version": "1.0", "provider_schemas": {"p": {"resource_schemas": {"a": {}}, "resource_schemas": {"b": {}}}}}`,
		`{"format_version": "1.0", "provider_schemas": {"p": {"resource_schemas": {"a": {}}}}, "provider_schemas": {"q": null}}`,
		`{"format_version": "1.0", "provider_schemas": {"p": {"resource_schemas": {"a": {}}}, "p": {"data_source_schemas": {"b": {}}}}}`,
		`{"format_version": "1.0", "provider_schemas": {"p": {"Resource_Schemas": {"a": {}}, "resource_schemas": null}}}`,
	} {
		f.Add([]byte(c))
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		got, err := parseRaw(doc, nil)
		var ps tfjson.ProviderSchemas
		werr := ps.UnmarshalJSON(doc)
		if (err == nil) != (werr == nil) {
			t.Fatalf("%q: parseRaw %v, whole decode %v", doc, err, werr)
		}
		if err != nil {
			return
		}
		if len(got.Schemas) != len(ps.Schemas) {
			t.Fatalf("%q: %d providers, whole decode %d", doc, len(got.Schemas), len(ps.Schemas))
		}
		for source, want := range ps.Schemas {
			p, ok := got.Schemas[source]
			if !ok || (p == nil) != (want == nil) {
				t.Fatalf("%q: provider %s differs", doc, source)
			}
			if p == nil {
				continue
			}
			sameTypes(t, doc, p.resources, want.ResourceSchemas)
			sameTypes(t, doc, p.dataSources, want.DataSourceSchemas)
		}
	})
}

// sameTypes fails when the types kept as JSON differ from the types of a
// whole decode, by name or by decoded block.
func sameTypes(t *testing.T, doc []byte, got map[string]json.RawMessage, want map[string]*tfjson.Schema) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%q: %d types, whole decode %d", doc, len(got), len(want))
	}
	for name, w := range want {
		raw, ok := got[name]
		if !ok {
			t.Fatalf("%q: type %s missing", doc, name)
		}
		g, err := decodeRawType(raw)
		if err != nil {
			t.Fatalf("%q: type %s: %v", doc, name, err)
		}
		if w == nil {
			w = &tfjson.Schema{}
		}
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("%q: type %s decodes to %+v, whole decode %+v", doc, name, g, w)
		}
	}
}
