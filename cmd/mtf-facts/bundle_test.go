package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fb "github.com/modulestf/facts/internal/bundle"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

const (
	testHead = "1111111111111111111111111111111111111111"
	testBase = "2222222222222222222222222222222222222222"
)

var testBinary = strings.Repeat("a", 64)

func fileSHA256(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeReport writes a small report bound to the test skills' registry.
func writeReport(t *testing.T, dir string) string {
	t.Helper()
	reg := fileSHA256(t, "../../testdata/bundle/skills/references/rule-facts.md")
	head, base := testHead, testBase
	r := facts.Report{
		Envelope: facts.Envelope{SchemaVersion: facts.SchemaVersion, Tool: facts.Tool{Name: "mtf-facts", BinarySHA256: testBinary},
			HeadSHA: &head, MergeBaseSHA: &base, RegistrySHA256: &reg,
			Inputs: facts.Inputs{Head: facts.TreeInput{Files: []facts.FileInput{}}, Base: &facts.TreeInput{Files: []facts.FileInput{}}}},
		Manifest: []facts.ManifestEntry{
			{RuleID: "structure.missing-description", Area: ".", Status: facts.StatusFinding},
			{RuleID: "coverage.unknown-argument", Area: ".", Status: facts.StatusNotEvaluated, Reason: "no schema", ReasonCode: facts.ReasonSchemaUnavailable},
		},
		Findings: []facts.Finding{{ID: "F-001", RuleID: "structure.missing-description", Severity: "MEDIUM", File: "variables.tf",
			Summary: "No description.", SuggestedFix: "Add one."}},
		Candidates: []facts.Candidate{},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "report.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func bundleArgsFor(report, skills, out string) []string {
	return []string{"bundle", "--report", report, "--skills", skills,
		"--out", filepath.Join(out, "bundle.txt"), "--injection", filepath.Join(out, "injection.md"),
		"--head-sha", testHead, "--merge-base-sha", testBase, "--binary-sha256", testBinary}
}

func runBundle(args []string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestBundleWritesJudgment(t *testing.T) {
	tmp := t.TempDir()
	report := writeReport(t, tmp)
	var first [2][]byte
	for i := range 2 {
		out := filepath.Join(tmp, fmt.Sprint("run", i))
		if err := os.Mkdir(out, 0o755); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runBundle(bundleArgsFor(report, "../../testdata/bundle/skills", out))
		if code != 0 || stdout != "judgment\n" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		b, err := os.ReadFile(filepath.Join(out, "bundle.txt"))
		if err != nil {
			t.Fatal(err)
		}
		inj, err := os.ReadFile(filepath.Join(out, "injection.md"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = [2][]byte{b, inj}
			if !strings.HasPrefix(string(b), "FACTS-BUNDLE-BYTES ") || !strings.Contains(string(inj), "coverage.unknown-argument") {
				t.Errorf("bundle %q, injection %q", b, inj)
			}
			continue
		}
		if !bytes.Equal(b, first[0]) || !bytes.Equal(inj, first[1]) {
			t.Error("two runs wrote different bytes")
		}
	}
}

func TestBundleRefuses(t *testing.T) {
	tmp := t.TempDir()
	report := writeReport(t, tmp)
	skills := "../../testdata/bundle/skills"
	replace := func(args []string, flag, value string) []string {
		out := append([]string{}, args...)
		for i := range out {
			if out[i] == flag {
				out[i+1] = value
			}
		}
		return out
	}
	cases := map[string]struct {
		args []string
		want string
	}{
		"head mismatch":       {replace(bundleArgsFor(report, skills, tmp), "--head-sha", testBase), "head_sha"},
		"merge base mismatch": {replace(bundleArgsFor(report, skills, tmp), "--merge-base-sha", testHead), "merge_base_sha"},
		"binary mismatch":     {replace(bundleArgsFor(report, skills, tmp), "--binary-sha256", strings.Repeat("b", 64)), "binary_sha256"},
		"profile unexpected":  {append(bundleArgsFor(report, skills, tmp), "--profile-dir", "../../testdata/profile"), "profile_sha256"},
		"schema unexpected":   {append(bundleArgsFor(report, skills, tmp), "--schema-handoff", "../../schema/examples/schema-handoff.valid.json"), "schema_inputs"},
		"bad head":            {replace(bundleArgsFor(report, skills, tmp), "--head-sha", "HEAD"), "--head-sha"},
		"bad binary":          {replace(bundleArgsFor(report, skills, tmp), "--binary-sha256", "x"), "--binary-sha256"},
		"no report":           {replace(bundleArgsFor(report, skills, tmp), "--report", ""), "required"},
		"extra argument":      {append(bundleArgsFor(report, skills, tmp), "x"), "unexpected argument"},
		"missing skills":      {replace(bundleArgsFor(report, skills, tmp), "--skills", tmp), "registry"},
		"same output twice":   {replace(bundleArgsFor(report, skills, tmp), "--injection", filepath.Join(tmp, ".", "bundle.txt")), "different files"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runBundle(c.args)
			if code != exitError || stdout != "" || !strings.Contains(stderr, c.want) {
				t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join(tmp, "bundle.txt")); err == nil {
				t.Error("a refused run wrote the bundle")
			}
		})
	}
	// An injection file that exists stops the run before the bundle is written.
	if err := os.WriteFile(filepath.Join(tmp, "injection.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBundle(bundleArgsFor(report, skills, tmp)); code != exitError || !strings.Contains(stderr, "exists") {
		t.Errorf("existing injection: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "bundle.txt")); err == nil {
		t.Error("the bundle was written beside an existing injection file")
	}
	if err := os.Remove(filepath.Join(tmp, "injection.md")); err != nil {
		t.Fatal(err)
	}
	// An output that exists, a planted link included, is never written.
	if err := os.Symlink(filepath.Join(tmp, "elsewhere"), filepath.Join(tmp, "bundle.txt")); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runBundle(bundleArgsFor(report, skills, tmp)); code != exitError || !strings.Contains(stderr, "exists") {
		t.Errorf("existing output: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "elsewhere")); err == nil {
		t.Error("the run wrote through a link")
	}
}

// TestBundleFromLint runs lint on a golden case with the pinned registry and
// builds the bundle from its report, with engine text made up for every rule.
func TestBundleFromLint(t *testing.T) {
	bundleFile, _, _ := bundleFromLint(t)
	f, err := os.Open(bundleFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := fb.Decode(f)
	if err != nil || len(b.Candidates) == 0 || b.Candidates[0].CandidateID != "C-001" {
		t.Errorf("bundle %+v, %v", b, err)
	}
}

// bundleFromLint writes the bundle of a lint run on a golden case with the
// pinned registry, and returns the bundle file, the lint report and the
// registry file. It skips without the registry, except in CI.
func bundleFromLint(t *testing.T) (bundleFile, report, regFile string) {
	t.Helper()
	regFile = os.Getenv(registryEnv)
	if regFile == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	regText, err := os.ReadFile(regFile) //nolint:gosec // G703: the path is the CI-set registry copy
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(string(regText))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	skills := filepath.Join(tmp, "skills")
	engine := filepath.Join(skills, "references", "engine")
	if err := os.MkdirAll(engine, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "references", "rule-facts.md"), regText, 0o644); err != nil { //nolint:gosec // G703: a test temp dir
		t.Fatal(err)
	}
	var text strings.Builder
	for _, r := range reg.Rules {
		if r.Engine != registry.EngineLLM {
			fmt.Fprintf(&text, "## `%s`\n\nText of %s.\n\n", r.ID, r.ID)
		}
	}
	if err := os.WriteFile(filepath.Join(engine, "all.md"), []byte(text.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := "../../testdata/compat-iface-positive"
	var lintOut, errb bytes.Buffer
	code := run([]string{"lint", "--json", filepath.Join(dir, "head"), "--base", filepath.Join(dir, "base"),
		"--registry", regFile, "--head-sha", testHead, "--merge-base-sha", testBase, "--profile-dir", "../../testdata/profile"}, &lintOut, &errb)
	if code == exitError {
		t.Fatalf("lint: %s", errb.String())
	}
	report = filepath.Join(tmp, "report.json")
	if err := os.WriteFile(report, lintOut.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	binary, err := binarySHA256()
	if err != nil {
		t.Fatal(err)
	}
	args := bundleArgsFor(report, skills, tmp)
	args[len(args)-1] = binary
	args = append(args, "--profile-dir", "../../testdata/profile")
	code, stdout, stderr := runBundle(args)
	if code != 0 || stdout != "judgment\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	return filepath.Join(tmp, "bundle.txt"), report, regFile
}
