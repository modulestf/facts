package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/schema"
)

const (
	oracleHeadSHA = "1111111111111111111111111111111111111111"
	oracleBaseSHA = "2222222222222222222222222222222222222222"
)

// oracleSchemaCases are the oracle fixtures with a schema handoff. Their
// handoffs record the .tf tree hashes of their head/ and base/ trees.
var oracleSchemaCases = []string{"coverage-positive", "coverage-negative", "compat-versions-floor"}

func oracleSchemaOptions(dir string) Options {
	return Options{
		Head: filepath.Join(dir, "head"), Base: filepath.Join(dir, "base"),
		HeadSHA: oracleHeadSHA, MergeBaseSHA: oracleBaseSHA,
		SchemaHandoff: filepath.Join(dir, "schema-handoff.json"), Limits: DefaultLimits,
	}
}

// runSchema runs applySchema alone and returns the set it gave the areas.
func runSchema(t *testing.T, o Options) (*SchemaInputs, *schema.Set) {
	t.Helper()
	head := mustTree(t, o.Head)
	var base *Tree
	if o.Base != "" {
		base = mustTree(t, o.Base)
	}
	areas := []*Area{{Path: "."}}
	in, err := applySchema(o, head, base, areas)
	if err != nil {
		t.Fatal(err)
	}
	return in, areas[0].Schema
}

// TestOracleHandoffsBindToTheirTrees checks tfTreeHash computes the tree
// hash the schema job records, on every oracle handoff.
func TestOracleHandoffsBindToTheirTrees(t *testing.T) {
	for _, name := range oracleSchemaCases {
		dir := filepath.Join("..", "..", "testdata", "oracle", name)
		in, s := runSchema(t, oracleSchemaOptions(dir))
		if s.Mismatch != "" {
			t.Errorf("%s: not bound: %s", name, s.Mismatch)
		}
		if r := s.Resolve(".", "aws"); r.Outcome != schema.Present {
			t.Errorf("%s: resolve %+v", name, r)
		}
		if in == nil || in.HeadSHA != oracleHeadSHA || in.MergeBaseSHA != oracleBaseSHA ||
			in.TreeHashes.Head != s.Handoff.Inputs.Head.TreeHash || len(in.HandoffSHA256) != 64 || in.SchemaFiles == nil {
			t.Errorf("%s: schema_inputs %+v", name, in)
		}
	}
}

// TestHandoffForOtherInputsIsUnavailable runs an oracle handoff against a
// run it does not cover, one difference at a time.
func TestHandoffForOtherInputsIsUnavailable(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "oracle", "coverage-positive")
	edited := copyTree(t, filepath.Join(dir, "head"))
	if err := os.WriteFile(filepath.Join(edited, "main.tf"), []byte("# edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := copyTree(t, filepath.Join(dir, "head"))
	if err := os.WriteFile(filepath.Join(added, "extra.tf"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Options){
		"head SHA":       func(o *Options) { o.HeadSHA = oracleBaseSHA },
		"merge-base SHA": func(o *Options) { o.MergeBaseSHA = oracleHeadSHA },
		"no head SHA":    func(o *Options) { o.HeadSHA = "" },
		"no base":        func(o *Options) { o.Base = "" },
		"base as head":   func(o *Options) { o.Head = o.Base },
		"head as base":   func(o *Options) { o.Base = o.Head },
		"edited file":    func(o *Options) { o.Head = edited },
		"added file":     func(o *Options) { o.Head = added },
	} {
		o := oracleSchemaOptions(dir)
		mutate(&o)
		in, s := runSchema(t, o)
		if s.Mismatch == "" {
			t.Errorf("%s: bound", name)
		}
		if r := s.Resolve(".", "aws"); r.Outcome != schema.Unavailable || r.Reason != ReasonSchemaUnavailable {
			t.Errorf("%s: resolve %+v", name, r)
		}
		if outcome, _ := s.Lookup(schema.Query{Source: "registry.terraform.io/hashicorp/aws", Version: "6.2.0", Type: "aws_demo_widget"}); outcome != schema.Unavailable {
			t.Errorf("%s: lookup %s", name, outcome)
		}
		// The envelope still records the handoff it read, for the host to compare.
		if in == nil || in.HandoffSHA256 == "" {
			t.Errorf("%s: no schema_inputs", name)
		}
	}
}

// TestSchemaInputsInTheEnvelope runs the engine with a handoff and a raw
// schema file and validates the envelope against its schema.
func TestSchemaInputsInTheEnvelope(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "oracle", "coverage-positive")
	raw := filepath.Join(t.TempDir(), "aws.json")
	if err := os.WriteFile(raw, []byte(`{"format_version": "1.0", "provider_schemas": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o := oracleSchemaOptions(dir)
	o.SchemaFiles = []string{raw}
	o.Version, o.BinarySHA256 = "test", strings.Repeat("0", 64)
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	in := r.Envelope.SchemaInputs
	if in == nil || len(in.SchemaFiles) != 1 || in.SchemaFiles[0].Path != raw || len(in.SchemaFiles[0].SHA256) != 64 {
		t.Fatalf("schema_inputs %+v", in)
	}
	data, err := json.Marshal(r.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if errs := loadSchemas(t).validate("envelope.schema.json", decodeJSON(t, data)); len(errs) > 0 {
		t.Errorf("envelope schema: %v\n%s", errs, data)
	}
	// Raw files alone: read, but there is no handoff to record.
	r, err = Run(Options{Head: o.Head, SchemaFiles: []string{raw}, Limits: DefaultLimits})
	if err != nil || r.Envelope.SchemaInputs != nil {
		t.Errorf("raw files alone: %v, schema_inputs %+v", err, r.Envelope.SchemaInputs)
	}
}

// TestBadSchemaInputsFailTheRun checks a missing or wrong schema input
// stops the run rather than reading as no input.
func TestBadSchemaInputsFailTheRun(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": "", "notes.txt": "x"})
	for name, o := range map[string]Options{
		"missing handoff":   {SchemaHandoff: filepath.Join(head, "missing.json")},
		"directory handoff": {SchemaHandoff: head},
		"text handoff":      {SchemaHandoff: filepath.Join(head, "notes.txt")},
		"missing file":      {SchemaFiles: []string{filepath.Join(head, "missing.json")}},
		"text file":         {SchemaFiles: []string{filepath.Join(head, "notes.txt")}},
	} {
		o.Head, o.Limits = head, DefaultLimits
		if _, err := Run(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil { //nolint:gosec // G703: a temp copy of a fixture
			t.Fatal(err)
		}
	}
	return dst
}
