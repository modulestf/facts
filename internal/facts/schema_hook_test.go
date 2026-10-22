package facts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
		if in == nil || in.TreeHashes == nil || deref(in.HeadSHA) != oracleHeadSHA || deref(in.MergeBaseSHA) != oracleBaseSHA ||
			in.TreeHashes.Head != s.Handoff.Inputs.Head.TreeHash || len(deref(in.HandoffSHA256)) != 64 || in.SchemaFiles == nil {
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
		if in == nil || deref(in.HandoffSHA256) == "" {
			t.Errorf("%s: no schema_inputs", name)
		}
	}
}

// TestSymlinksAreLeftOutOfTheTreeHash adds .tf links to an oracle head: the
// tree hash stays the one the handoff records, so the handoff still binds.
func TestSymlinksAreLeftOutOfTheTreeHash(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "oracle", "coverage-positive")
	head := copyTree(t, filepath.Join(dir, "head"))
	want := tfTreeHash(mustTree(t, head))
	for link, target := range map[string]string{"link.tf": "main.tf", "dangling.tf": "missing.tf", "outside.tf": "../outside.tf"} {
		if err := os.Symlink(target, filepath.Join(head, link)); err != nil {
			t.Fatal(err)
		}
	}
	tree := mustTree(t, head)
	if tree.Entries["link.tf"] == nil || tree.Entries["link.tf"].Kind != KindSymlink {
		t.Fatal("link.tf is not a link entry")
	}
	if got := tfTreeHash(tree); got != want {
		t.Errorf("tree hash with links %s, want %s", got, want)
	}
	o := oracleSchemaOptions(dir)
	o.Head = head
	if _, s := runSchema(t, o); s.Mismatch != "" {
		t.Errorf("not bound: %s", s.Mismatch)
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
}

// TestRawSchemaFilesInTheEnvelope runs the engine with raw schema files and
// no handoff: each file's hash is recorded, the handoff keys are null, and the
// envelope schema accepts that and refuses the mutations that break it.
func TestRawSchemaFilesInTheEnvelope(t *testing.T) {
	dir := t.TempDir()
	raws := []string{filepath.Join(dir, "aws.json"), filepath.Join(dir, "random.json")}
	for i, p := range raws {
		doc := fmt.Sprintf(`{"format_version": "1.0", "provider_schemas": {}, "n": %d}`, i)
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	head := filepath.Join("..", "..", "testdata", "oracle", "coverage-positive", "head")
	o := Options{Head: head, SchemaFiles: raws, Limits: DefaultLimits, Version: "test", BinarySHA256: strings.Repeat("0", 64)}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	in := r.Envelope.SchemaInputs
	if in == nil || in.HandoffSHA256 != nil || in.HeadSHA != nil || in.MergeBaseSHA != nil || in.TreeHashes != nil {
		t.Fatalf("schema_inputs %+v", in)
	}
	if len(in.SchemaFiles) != len(raws) {
		t.Fatalf("schema_files %+v", in.SchemaFiles)
	}
	for i, f := range in.SchemaFiles {
		data, err := os.ReadFile(raws[i])
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%x", sha256.Sum256(data)); f.Path != raws[i] || f.SHA256 != want {
			t.Errorf("schema_files[%d] = %+v, want %s %s", i, f, raws[i], want)
		}
	}
	data, err := json.Marshal(r.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	set := loadSchemas(t)
	if errs := set.validate("envelope.schema.json", decodeJSON(t, data)); len(errs) > 0 {
		t.Errorf("envelope schema: %v\n%s", errs, data)
	}

	sum := strings.Repeat("c", 64)
	sha := strings.Repeat("a", 40)
	for name, mutate := range map[string]func(*SchemaInputs){
		"no schema files":         func(s *SchemaInputs) { s.SchemaFiles = []FileHash{} },
		"handoff hash only":       func(s *SchemaInputs) { s.HandoffSHA256 = &sum },
		"commits only":            func(s *SchemaInputs) { s.HeadSHA, s.MergeBaseSHA = &sha, &sha },
		"tree hashes only":        func(s *SchemaInputs) { s.TreeHashes = &TreeHashes{Head: sum, MergeBase: sum} },
		"handoff, no tree hashes": func(s *SchemaInputs) { s.HandoffSHA256, s.HeadSHA, s.MergeBaseSHA = &sum, &sha, &sha },
	} {
		env := r.Envelope
		si := *in
		mutate(&si)
		env.SchemaInputs = &si
		if errs := set.validate("envelope.schema.json", decodeJSON(t, mustJSON(t, env))); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
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
