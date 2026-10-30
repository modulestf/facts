package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// matching returns an envelope and the pins it satisfies, with every
// optional binding present.
func matching() (facts.Envelope, Pins) {
	schemaInputs := func() *facts.SchemaInputs {
		return &facts.SchemaInputs{
			HandoffSHA256: new(strings.Repeat("c", 64)), HeadSHA: new(headSHA), MergeBaseSHA: new(baseSHA),
			TreeHashes:  &facts.TreeHashes{Head: strings.Repeat("d", 64), MergeBase: strings.Repeat("e", 64)},
			SchemaFiles: []facts.FileHash{{Path: "aws.json", SHA256: strings.Repeat("f", 64)}},
		}
	}
	env := facts.Envelope{
		SchemaVersion: facts.SchemaVersion,
		Tool:          facts.Tool{BinarySHA256: strings.Repeat("a", 64)},
		HeadSHA:       new(headSHA), MergeBaseSHA: new(baseSHA), RegistrySHA256: new(strings.Repeat("b", 64)),
		SchemaInputs: schemaInputs(), ProfileSHA256: new(strings.Repeat("9", 64)),
	}
	pins := Pins{HeadSHA: headSHA, MergeBaseSHA: baseSHA, BinarySHA256: strings.Repeat("a", 64),
		RegistrySHA256: strings.Repeat("b", 64), Schema: schemaInputs(), ProfileSHA256: new(strings.Repeat("9", 64))}
	return env, pins
}

func TestCheckAcceptsMatchingEnvelope(t *testing.T) {
	env, pins := matching()
	if err := Check(env, pins); err != nil {
		t.Fatal(err)
	}
	env.SchemaInputs, pins.Schema, env.ProfileSHA256, pins.ProfileSHA256 = nil, nil, nil, nil
	if err := Check(env, pins); err != nil {
		t.Errorf("no schema and no profile: %v", err)
	}
}

// TestCheckRefusesEachField changes one field at a time, on either side, and
// checks the predicate refuses it and names it.
func TestCheckRefusesEachField(t *testing.T) {
	other := strings.Repeat("0", 64)
	cases := []struct {
		name, field string
		mutate      func(*facts.Envelope, *Pins)
	}{
		{"schema version", "schema_version", func(e *facts.Envelope, _ *Pins) { e.SchemaVersion++ }},
		{"head", "head_sha", func(_ *facts.Envelope, p *Pins) { p.HeadSHA = baseSHA }},
		{"head missing", "head_sha", func(e *facts.Envelope, _ *Pins) { e.HeadSHA = nil }},
		{"merge base", "merge_base_sha", func(_ *facts.Envelope, p *Pins) { p.MergeBaseSHA = headSHA }},
		{"merge base missing", "merge_base_sha", func(e *facts.Envelope, _ *Pins) { e.MergeBaseSHA = nil }},
		{"binary", "tool.binary_sha256", func(_ *facts.Envelope, p *Pins) { p.BinarySHA256 = other }},
		{"registry", "registry_sha256", func(_ *facts.Envelope, p *Pins) { p.RegistrySHA256 = other }},
		{"registry missing", "registry_sha256", func(e *facts.Envelope, _ *Pins) { e.RegistrySHA256 = nil }},
		{"profile", "profile_sha256", func(_ *facts.Envelope, p *Pins) { p.ProfileSHA256 = &other }},
		{"profile not read", "profile_sha256", func(e *facts.Envelope, _ *Pins) { e.ProfileSHA256 = nil }},
		{"profile not given", "profile_sha256", func(_ *facts.Envelope, p *Pins) { p.ProfileSHA256 = nil }},
		{"schema not read", "schema_inputs present", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs = nil }},
		{"schema not given", "schema_inputs present", func(_ *facts.Envelope, p *Pins) { p.Schema = nil }},
		{"handoff hash", "schema_inputs.handoff_sha256", func(_ *facts.Envelope, p *Pins) { p.Schema.HandoffSHA256 = &other }},
		{"handoff head", "schema_inputs.head_sha", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs.HeadSHA = new(baseSHA) }},
		{"handoff merge base", "schema_inputs.merge_base_sha", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs.MergeBaseSHA = new(headSHA) }},
		{"tree hash", "schema_inputs.tree_hashes", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs.TreeHashes.MergeBase = other }},
		{"schema file hash", "schema_inputs.schema_files", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs.SchemaFiles[0].SHA256 = other }},
		{"schema file name", "schema_inputs.schema_files", func(e *facts.Envelope, _ *Pins) { e.SchemaInputs.SchemaFiles[0].Path = "b.json" }},
		{"schema file extra", "schema_inputs.schema_files", func(e *facts.Envelope, _ *Pins) {
			e.SchemaInputs.SchemaFiles = append(e.SchemaInputs.SchemaFiles, facts.FileHash{Path: "b.json", SHA256: other})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, pins := matching()
			c.mutate(&env, &pins)
			err := Check(env, pins)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.field) {
				t.Errorf("err %v, want one naming %s", err, c.field)
			}
		})
	}
}

func TestSchemaPinsMatchLint(t *testing.T) {
	if in, err := SchemaPins("", nil); in != nil || err != nil {
		t.Errorf("no schema input: %v, %v", in, err)
	}
	handoff := "../../schema/examples/schema-handoff.valid.json"
	in, err := SchemaPins(handoff, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := facts.Run(facts.Options{Head: "../../testdata/versions-missing-positive/head", SchemaHandoff: handoff, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	env, pins := matching()
	env.SchemaInputs, pins.Schema = r.Envelope.SchemaInputs, in
	if err := Check(env, pins); err != nil {
		t.Errorf("pins from the same handoff: %v", err)
	}
	if _, err := SchemaPins(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Error("a missing handoff loaded")
	}
}

func TestProfileSHA256MatchesLint(t *testing.T) {
	dir := "../../testdata/profile"
	got, err := ProfileSHA256(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := facts.Run(facts.Options{Head: "../../testdata/versions-missing-positive/head", ProfileDir: dir, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || r.Envelope.ProfileSHA256 == nil || *got != *r.Envelope.ProfileSHA256 {
		t.Errorf("profile sha256 %v, lint %v", show(got), show(r.Envelope.ProfileSHA256))
	}
	// A directory with no profile records none, as lint does.
	if got, err := ProfileSHA256(t.TempDir()); got != nil || err != nil {
		t.Errorf("empty directory: %v, %v", show(got), err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ProfileSHA256(file); err == nil {
		t.Error("a file passed as the profile directory")
	}
}

// TestReadReportRefusesAI refuses a lint --ai report by name,
// before the strict decode trips on its first ai key.
func TestReadReportRefusesAI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.json")
	doc := `{"envelope":{"inputs":{"head":{"tree_sha256":"","file_count":1}}},"manifest_summary":{}}`
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(p); !errors.Is(err, facts.ErrAIReport) {
		t.Errorf("AI report gives %v", err)
	}
}
