package schema

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	headSHA = "1111111111111111111111111111111111111111"
	baseSHA = "2222222222222222222222222222222222222222"
	aws     = "registry.terraform.io/hashicorp/aws"
)

// oracleHandoffs are the coverage handoffs of the oracle fixtures.
var oracleHandoffs = []string{"coverage-positive", "coverage-negative", "compat-versions-floor"}

func oracleHandoff(name string) string {
	return filepath.Join("..", "..", "testdata", "oracle", name, "schema-handoff.json")
}

// bound loads p and binds it to the commits and tree hashes it records.
func bound(t *testing.T, p string) *Set {
	t.Helper()
	s, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Bind(matching(s))
	if s.Mismatch != "" {
		t.Fatalf("%s: not bound: %s", p, s.Mismatch)
	}
	return s
}

func matching(s *Set) Run {
	h := s.Handoff
	return Run{HeadSHA: h.HeadSHA, MergeBaseSHA: h.MergeBaseSHA,
		HeadTreeHash: h.Inputs.Head.TreeHash, BaseTreeHash: h.Inputs.MergeBase.TreeHash}
}

func TestOracleHandoffs(t *testing.T) {
	for _, name := range oracleHandoffs {
		s := bound(t, oracleHandoff(name))
		if s.Handoff.HeadSHA != headSHA || s.Handoff.MergeBaseSHA != baseSHA {
			t.Errorf("%s: commits %s %s", name, s.Handoff.HeadSHA, s.Handoff.MergeBaseSHA)
		}
		res := s.Resolve(".", "aws")
		if res.Outcome != Present || res.Source != aws || res.Versions.Latest == "" || res.Versions.DeclaredMinimum == "" {
			t.Fatalf("%s: resolve %+v", name, res)
		}
		for _, v := range []string{res.Versions.Latest, res.Versions.DeclaredMinimum} {
			if o, _ := s.Lookup(Query{Source: aws, Version: v, Type: "aws_demo_widget"}); o != Present {
				t.Errorf("%s: aws_demo_widget at %s: %s", name, v, o)
			}
		}
		latest := Query{Source: aws, Version: res.Versions.Latest, Type: "aws_demo_widget"}
		for path, want := range map[string]Outcome{
			"name":       Present,
			"bogus_flag": AbsentFromSchema,
			"name/x":     AbsentFromSchema,
		} {
			q := latest
			q.Path = strings.Split(path, "/")
			if o, _ := s.Lookup(q); o != want {
				t.Errorf("%s: %s: %s, want %s", name, path, o, want)
			}
		}
		q := latest
		q.Type = "aws_no_such_type"
		if o, _ := s.Lookup(q); o != AbsentFromSchema {
			t.Errorf("%s: unknown type: %s", name, o)
		}
		q = latest
		q.Version = "0.0.1"
		if o, _ := s.Lookup(q); o != Unavailable {
			t.Errorf("%s: unknown version: %s", name, o)
		}
		if r := s.Resolve("modules/none", "aws"); r.Outcome != Unavailable || r.Reason != "schema_unavailable" {
			t.Errorf("%s: unknown directory: %+v", name, r)
		}
	}
}

func TestLookupFields(t *testing.T) {
	s := bound(t, "../../schema/examples/schema-handoff.valid.json")
	q := func(path ...string) Query {
		return Query{Source: aws, Version: "6.20.0", Type: "aws_example_thing", Path: path}
	}
	if o, f := s.Lookup(q("name")); o != Present || f.Attribute == nil || !f.Attribute.Required {
		t.Errorf("name: %s %+v", o, f)
	}
	if o, f := s.Lookup(q("legacy_flag")); o != Present || !f.Attribute.Deprecated {
		t.Errorf("legacy_flag: %s %+v", o, f)
	}
	if o, f := s.Lookup(q("rules", "port")); o != Present || !f.Attribute.Required {
		t.Errorf("rules.port: %s %+v", o, f)
	}
	if o, f := s.Lookup(q("setting")); o != Present || f.BlockType == nil || f.BlockType.MaxItems != 1 || f.BlockType.NestingMode != "list" {
		t.Errorf("setting: %s %+v", o, f)
	}
	if o, _ := s.Lookup(q("setting", "value")); o != Present {
		t.Errorf("setting.value: %s", o)
	}
	if o, f := s.Lookup(q()); o != Present || f.Block == nil || f.Attribute != nil {
		t.Errorf("type: %s %+v", o, f)
	}
	if o, _ := s.Lookup(Query{Source: aws, Version: "6.20.0", DataSource: true, Type: "aws_example_thing"}); o != AbsentFromSchema {
		t.Errorf("data source: %s", o)
	}
}

// TestUnresolvedReasonPassesThrough reads the reason the schema job gave.
func TestUnresolvedReasonPassesThrough(t *testing.T) {
	s := bound(t, "../../schema/examples/schema-handoff.valid.json")
	r := s.Resolve("examples/complete", "aws")
	if r.Outcome != Unavailable || r.Reason != "constraint_unparsable" {
		t.Errorf("resolve %+v", r)
	}
	r = s.Resolve(".", "aws")
	if r.Outcome != Present || r.Versions.OldFloor == nil || *r.Versions.OldFloor != "5.95.0" {
		t.Errorf("resolve %+v", r)
	}
}

// TestMismatchIsUnavailable changes each bound field of the run in turn:
// every handoff lookup is then schema_unavailable, never present or absent.
func TestMismatchIsUnavailable(t *testing.T) {
	for _, name := range oracleHandoffs {
		s := bound(t, oracleHandoff(name))
		good := matching(s)
		version := s.Resolve(".", "aws").Versions.Latest
		for field, mutate := range map[string]func(*Run){
			"head SHA":             func(r *Run) { r.HeadSHA = baseSHA },
			"merge-base SHA":       func(r *Run) { r.MergeBaseSHA = headSHA },
			"head tree hash":       func(r *Run) { r.HeadTreeHash = r.BaseTreeHash },
			"merge-base tree hash": func(r *Run) { r.BaseTreeHash = strings.Repeat("0", 64) },
			"no head SHA":          func(r *Run) { r.HeadSHA = "" },
			"no base tree":         func(r *Run) { r.BaseTreeHash = "" },
		} {
			r := good
			mutate(&r)
			if r == good {
				t.Fatalf("%s: mutation %s changed nothing", name, field)
			}
			s.Bind(r)
			if s.Mismatch == "" {
				t.Errorf("%s: %s: bound", name, field)
			}
			if res := s.Resolve(".", "aws"); res.Outcome != Unavailable || res.Reason != "schema_unavailable" {
				t.Errorf("%s: %s: resolve %+v", name, field, res)
			}
			for _, typ := range []string{"aws_demo_widget", "aws_no_such_type"} {
				if o, _ := s.Lookup(Query{Source: aws, Version: version, Type: typ}); o != Unavailable {
					t.Errorf("%s: %s: %s: %s", name, field, typ, o)
				}
			}
		}
		s.Bind(good)
		if o, _ := s.Lookup(Query{Source: aws, Version: version, Type: "aws_demo_widget"}); o != Present {
			t.Errorf("%s: rebinding: %s", name, o)
		}
	}
}

func TestNilSetIsUnavailable(t *testing.T) {
	var s *Set
	if o, _ := s.Lookup(Query{Source: aws, Type: "aws_demo_widget"}); o != Unavailable {
		t.Errorf("lookup %s", o)
	}
	if r := s.Resolve(".", "aws"); r.Outcome != Unavailable {
		t.Errorf("resolve %+v", r)
	}
}

func TestGzippedHandoff(t *testing.T) {
	data, err := os.ReadFile(oracleHandoff("coverage-positive"))
	if err != nil {
		t.Fatal(err)
	}
	p := writeFile(t, "h.json.gz", gzipped(t, data))
	s := bound(t, p)
	plain := bound(t, oracleHandoff("coverage-positive"))
	if s.HandoffSHA256 == plain.HandoffSHA256 || len(s.HandoffSHA256) != 64 {
		t.Errorf("hash %s, plain %s: want the hash of the file as read", s.HandoffSHA256, plain.HandoffSHA256)
	}
	if !reflect.DeepEqual(s.Resolve(".", "aws"), plain.Resolve(".", "aws")) {
		t.Errorf("gzipped handoff reads differently")
	}
}

func TestSchemaExamples(t *testing.T) {
	if _, err := Load("../../schema/examples/schema-handoff.valid.json", nil); err != nil {
		t.Errorf("valid example: %v", err)
	}
	invalid, _ := filepath.Glob("../../schema/examples/schema-handoff.invalid.*.json")
	if len(invalid) == 0 {
		t.Fatal("no invalid examples")
	}
	for _, p := range invalid {
		if _, err := Load(p, nil); err == nil {
			t.Errorf("%s: accepted", filepath.Base(p))
		}
	}
}

// TestMalformedHandoffs mutates the valid example one way at a time; each
// mutation must be refused.
func TestMalformedHandoffs(t *testing.T) {
	data, err := os.ReadFile("../../schema/examples/schema-handoff.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	dir0 := `{"path": ".", "providers": [`
	for name, mutate := range map[string]func(string) string{
		"not JSON":       func(string) string { return "{" },
		"trailing data":  func(s string) string { return s + "{}" },
		"unknown member": func(s string) string { return strings.Replace(s, `"caps"`, `"extra": 1, "caps"`, 1) },
		"short head SHA": func(s string) string { return strings.Replace(s, headSHA, "1111", 1) },
		"upper base SHA": func(s string) string { return strings.Replace(s, baseSHA, strings.Repeat("A", 40), 1) },
		"zero cap": func(s string) string {
			return strings.Replace(s, `"compressed_bytes": 2097152`, `"compressed_bytes": 0`, 1)
		},
		"own cap exceeded": func(s string) string {
			return strings.Replace(s, `"uncompressed_bytes": 33554432`, `"uncompressed_bytes": 100`, 1)
		},
		"bad tree hash":  func(s string) string { return strings.Replace(s, `"tree_hash": "aaaa`, `"tree_hash": "AAAA`, 1) },
		"absolute file":  func(s string) string { return strings.Replace(s, `"path": "main.tf"`, `"path": "/main.tf"`, 1) },
		"dotdot file":    func(s string) string { return strings.Replace(s, `"path": "main.tf"`, `"path": "../main.tf"`, 1) },
		"not a .tf file": func(s string) string { return strings.Replace(s, `"path": "main.tf"`, `"path": "main.tf.json"`, 1) },
		"repeated file":  func(s string) string { return strings.Replace(s, `"path": "versions.tf"`, `"path": "main.tf"`, 1) },
		"repeated directory": func(s string) string {
			return strings.Replace(s, `{"path": "examples/complete"`, `{"path": "."`, 1)
		},
		"trailing slash directory": func(s string) string {
			return strings.Replace(s, `"examples/complete"`, `"examples/complete/"`, 1)
		},
		"repeated local name": func(s string) string {
			return strings.Replace(s, dir0, dir0+`{"local_name": "aws", "source": "`+aws+`", "unresolved_reason": "x"}, `, 1)
		},
		"versions and reason": func(s string) string {
			return strings.Replace(s, `"old_floor": "5.95.0"}`, `"old_floor": "5.95.0"}, "unresolved_reason": "x"`, 1)
		},
		"neither": func(s string) string {
			return strings.Replace(s, `, "unresolved_reason": "constraint_unparsable"`, ``, 1)
		},
		"no old_floor": func(s string) string { return strings.Replace(s, `, "old_floor": "5.95.0"`, ``, 1) },
		"bad version":  func(s string) string { return strings.Replace(s, `"latest": "6.20.0"`, `"latest": "6.20"`, 1) },
		"minimum above": func(s string) string {
			return strings.Replace(s, `"declared_minimum": "6.0.0"`, `"declared_minimum": "7.0.0"`, 1)
		},
		"bad reason":     func(s string) string { return strings.Replace(s, `"constraint_unparsable"`, `"Bad Reason"`, 1) },
		"bad local name": func(s string) string { return strings.Replace(s, `"local_name": "aws"`, `"local_name": "AWS"`, 1) },
		"bad source":     func(s string) string { return strings.Replace(s, `"source": "`+aws, `"source": "aws`, 1) },
		"bad type name":  func(s string) string { return strings.Replace(s, `"aws_example_thing"`, `"Aws-Thing"`, 1) },
		"type version": func(s string) string {
			return strings.Replace(s, `"aws_example_thing": {"block"`, `"aws_example_thing": {"version": 1, "block"`, 1)
		},
		"attribute type": func(s string) string {
			return strings.Replace(s, `"name": {"required": true}`, `"name": {"required": true, "type": "string"}`, 1)
		},
		"sensitive": func(s string) string {
			return strings.Replace(s, `"name": {"required": true}`, `"name": {"required": true, "sensitive": true}`, 1)
		},
		"string flag": func(s string) string {
			return strings.Replace(s, `"name": {"required": true}`, `"name": {"required": "yes"}`, 1)
		},
		"null attribute": func(s string) string { return strings.Replace(s, `"name": {"required": true}`, `"name": null`, 1) },
		"block mode": func(s string) string {
			return strings.Replace(s, `"nesting_mode": "list", "max_items": 1, "block"`, `"nesting_mode": "tuple", "max_items": 1, "block"`, 1)
		},
		"negative items": func(s string) string { return strings.Replace(s, `"max_items": 1,`, `"max_items": -1,`, 1) },
		"fraction items": func(s string) string { return strings.Replace(s, `"max_items": 1,`, `"max_items": 1.5,`, 1) },
		"repeated version": func(s string) string {
			return strings.Replace(s, `"provider_schemas": [`, `"provider_schemas": [`+schemaEntry(s)+`, `, 1)
		},
		"no inputs":        func(s string) string { return edit(s, func(d map[string]any) { delete(d, "inputs") }) },
		"no caps":          func(s string) string { return edit(s, func(d map[string]any) { delete(d, "caps") }) },
		"null directories": func(s string) string { return edit(s, func(d map[string]any) { d["directories"] = nil }) },
		"no head files": func(s string) string {
			return edit(s, func(d map[string]any) { delete(obj(d, "inputs", "head"), "files") })
		},
		"no providers": func(s string) string {
			return edit(s, func(d map[string]any) { delete(item(d, "directories"), "providers") })
		},
		"no data sources": func(s string) string {
			return edit(s, func(d map[string]any) { delete(item(d, "provider_schemas"), "data_source_schemas") })
		},
		"null block": func(s string) string {
			return edit(s, func(d map[string]any) {
				obj(item(d, "provider_schemas"), "resource_schemas", "aws_example_thing")["block"] = nil
			})
		},
		"no nested attrs": func(s string) string {
			return edit(s, func(d map[string]any) {
				delete(obj(thing(d), "attributes", "rules", "nested_type"), "attributes")
			})
		},
		"no block in block type": func(s string) string {
			return edit(s, func(d map[string]any) { delete(obj(thing(d), "block_types", "setting"), "block") })
		},
		"nested deprecated": func(s string) string {
			return strings.Replace(s, `"nested_type": {"nesting_mode"`, `"nested_type": {"deprecated": true, "nesting_mode"`, 1)
		},
	} {
		mutated := mutate(string(data))
		if mutated == string(data) {
			t.Errorf("%s: mutation changed nothing", name)
			continue
		}
		if _, err := Load(writeFile(t, "h.json", []byte(mutated)), nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// edit decodes the document s, applies f and encodes it again.
func edit(s string, f func(map[string]any)) string {
	var d map[string]any
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		panic(err)
	}
	f(d)
	out, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// obj walks d by keys to an object.
func obj(d map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		d = d[k].(map[string]any)
	}
	return d
}

// item returns the first object of the array d[key].
func item(d map[string]any, key string) map[string]any { return d[key].([]any)[0].(map[string]any) }

// thing returns the block of the example's aws_example_thing.
func thing(d map[string]any) map[string]any {
	return obj(item(d, "provider_schemas"), "resource_schemas", "aws_example_thing", "block")
}

// schemaEntry returns the provider_schemas entry of the valid example as
// text, for a duplicate.
func schemaEntry(s string) string {
	var h struct {
		ProviderSchemas []json.RawMessage `json:"provider_schemas"`
	}
	if err := json.Unmarshal([]byte(s), &h); err != nil || len(h.ProviderSchemas) == 0 {
		return ""
	}
	return string(h.ProviderSchemas[0])
}

func TestHandoffCaps(t *testing.T) {
	data, err := os.ReadFile(oracleHandoff("coverage-positive"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzipped(t, data)
	defer func(c, u int64) { maxCompressedBytes, maxHandoffBytes = c, u }(maxCompressedBytes, maxHandoffBytes)

	maxCompressedBytes, maxHandoffBytes = int64(len(gz)), int64(len(data))
	for _, p := range []string{writeFile(t, "h.json", data), writeFile(t, "h.json.gz", gz)} {
		if _, err := Load(p, nil); err != nil {
			t.Errorf("at the caps: %v", err)
		}
	}
	maxHandoffBytes = int64(len(data)) - 1
	for _, p := range []string{writeFile(t, "h.json", data), writeFile(t, "h.json.gz", gz)} {
		if _, err := Load(p, nil); err == nil || !strings.Contains(err.Error(), "over") {
			t.Errorf("%s over the uncompressed cap: %v", filepath.Base(p), err)
		}
	}
	maxCompressedBytes, maxHandoffBytes = int64(len(gz))-1, int64(len(data))
	if _, err := Load(writeFile(t, "h.json.gz", gz), nil); err == nil || !strings.Contains(err.Error(), "over") {
		t.Errorf("over the compressed cap: %v", err)
	}
}

func TestHandoffMustBeARegularFile(t *testing.T) {
	dir := t.TempDir()
	target, err := filepath.Abs(oracleHandoff("coverage-positive"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "missing.json"), dir, link} {
		if _, err := Load(p, nil); err == nil {
			t.Errorf("%s: accepted", p)
		}
	}
}

const rawSchema = `{
  "format_version": "1.0",
  "provider_schemas": {
    "registry.terraform.io/hashicorp/aws": {
      "resource_schemas": {
        "aws_demo_widget": {"version": 0, "block": {
          "attributes": {
            "name": {"type": "string", "required": true, "description": "kept"},
            "secret": {"type": "string", "optional": true, "sensitive": true},
            "tags": {"type": ["map", "string"], "optional": true}
          },
          "block_types": {"rule": {"nesting_mode": "set", "block": {"attributes": {"port": {"type": "number", "required": true}}}}}
        }}
      },
      "data_source_schemas": {}
    }
  }
}`

func TestRawSchemaFiles(t *testing.T) {
	p := writeFile(t, "aws.json", []byte(rawSchema))
	s, err := Load("", []string{p})
	if err != nil {
		t.Fatal(err)
	}
	if s.Handoff != nil || len(s.SchemaFiles) != 1 || s.SchemaFiles[0].Path != p || len(s.SchemaFiles[0].SHA256) != 64 {
		t.Errorf("load %+v", s)
	}
	q := Query{Source: aws, Type: "aws_demo_widget", Path: []string{"secret"}}
	if o, f := s.Lookup(q); o != Present || !f.Attribute.Sensitive || f.Attribute.AttributeType.FriendlyName() != "string" {
		t.Errorf("secret: %s %+v", o, f)
	}
	q.Path = []string{"tags"}
	if o, f := s.Lookup(q); o != Present || !f.Attribute.AttributeType.IsMapType() {
		t.Errorf("tags: %s %+v", o, f)
	}
	q.Path = []string{"rule", "port"}
	if o, _ := s.Lookup(q); o != Present {
		t.Errorf("rule.port: %s", o)
	}
	q.Path = []string{"bogus"}
	if o, _ := s.Lookup(q); o != AbsentFromSchema {
		t.Errorf("bogus: %s", o)
	}
	// Raw files need no binding, and a version reads the handoff, not them.
	q.Path, q.Version = nil, "6.0.0"
	if o, _ := s.Lookup(q); o != Unavailable {
		t.Errorf("versioned query on raw files: %s", o)
	}
	if o, _ := s.Lookup(Query{Source: "registry.terraform.io/hashicorp/random", Type: "random_id"}); o != Unavailable {
		t.Errorf("unknown provider: %s", o)
	}
}

func TestRawSchemaFilesRefused(t *testing.T) {
	good := writeFile(t, "aws.json", []byte(rawSchema))
	for name, files := range map[string][]string{
		"not JSON":        {writeFile(t, "a.json", []byte("{"))},
		"unknown format":  {writeFile(t, "a.json", []byte(strings.Replace(rawSchema, `"1.0"`, `"9.0"`, 1)))},
		"no format":       {writeFile(t, "a.json", []byte(`{"provider_schemas": {}}`))},
		"repeated source": {good, writeFile(t, "b.json", []byte(rawSchema))},
		"missing":         {good, filepath.Join(t.TempDir(), "missing.json")},
		"directory":       {t.TempDir()},
		"too many":        slices.Repeat([]string{good}, maxSchemaFiles+1),
	} {
		if _, err := Load("", files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	defer func(n int64) { maxSchemaFileBytes = n }(maxSchemaFileBytes)
	maxSchemaFileBytes = int64(len(rawSchema)) - 1
	if _, err := Load("", []string{good}); err == nil {
		t.Error("over the cap: accepted")
	}
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil { //nolint:gosec // G703: a file in the test temp directory
		t.Fatal(err)
	}
	return p
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
