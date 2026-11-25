package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// featuresGolden lists every feature name a release has shipped. A name
// may be added to it, never removed: scripts test for these names.
const featuresGolden = "testdata/features.txt"

// TestVersionPlain checks version and --version still print the one line
// they printed before version --json existed.
func TestVersionPlain(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, out, errOut := runCLI(args)
		if code != exitClean || errOut != "" || out != version()+"\n" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

// decodeVersionReport runs version --json and decodes its stdout strictly.
func decodeVersionReport(t *testing.T) versionReport {
	t.Helper()
	code, out, errOut := runCLI([]string{"version", "--json"})
	if code != exitClean || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want one line of JSON, got %q", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	var r versionReport
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("decode: %v: %s", err, out)
	}
	return r
}

func TestVersionJSON(t *testing.T) {
	r := decodeVersionReport(t)
	if r.SchemaVersion != versionSchemaVersion || r.Name != "mtf-facts" || r.Version != version() || r.Go == "" {
		t.Errorf("envelope: %+v", r)
	}
	if r.Commit == nil && r.Modified != nil {
		t.Errorf("modified %v without a commit", *r.Modified)
	}
	if len(r.Features) == 0 || !slices.IsSorted(r.Features) || len(slices.Compact(slices.Clone(r.Features))) != len(r.Features) {
		t.Errorf("features are not sorted and unique: %q", r.Features)
	}
	// Every name has the grammar the schema states.
	var s struct {
		Properties struct {
			Features struct {
				Items struct {
					Pattern string `json:"pattern"`
				} `json:"items"`
			} `json:"features"`
		} `json:"properties"`
	}
	readJSON(t, "../../schema/version.schema.json", &s)
	re := regexp.MustCompile(s.Properties.Features.Items.Pattern)
	for _, f := range r.Features {
		if !re.MatchString(f) {
			t.Errorf("feature %q does not match the schema pattern %s", f, re)
		}
	}
}

// TestVersionJSONRefused checks a stray argument is a usage error with no report.
func TestVersionJSONRefused(t *testing.T) {
	code, out, _ := runCLI([]string{"version", "--json", "extra"})
	if code != exitError || out != "" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
}

// TestFeaturesGolden fails when a shipped feature name disappears, and when
// a new one is not yet in the golden file, so every addition is reviewed.
func TestFeaturesGolden(t *testing.T) {
	f, err := os.Open(featuresGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var golden []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := sc.Text(); l != "" && !strings.HasPrefix(l, "#") {
			golden = append(golden, l)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	got := decodeVersionReport(t).Features
	for _, g := range golden {
		if !slices.Contains(got, g) {
			t.Errorf("shipped feature %q is gone: scripts test for it, keep it", g)
		}
	}
	for _, g := range got {
		if !slices.Contains(golden, g) {
			t.Errorf("feature %q is not in %s: add it there", g, featuresGolden)
		}
	}
}

// TestFeaturesProbed lists the feature each script probe of the skills
// repository maps to, so the probe can move to version --json.
func TestFeaturesProbed(t *testing.T) {
	probes := map[string]string{
		"cmd:profile-check":                "profile-check --help | grep 'profile-check --profile-dir'",
		"flag:profile-check:gates":         "profile-check --gates",
		"flag:profile-check:downgrade-ids": "profile-check --downgrade-ids",
		"cmd:profile-snapshot":             "profile-snapshot before profile-check",
		"flag:fixture-check:expect":        "fixture-check --expect, read from exit 2",
		"flag:fixture-check:pins":          "fixture-check --pins at L3",
		"flag:fixture-check:resource":      "fixture-check --resource at L3",
		"flag:fixture-check:schema":        "fixture-check --schema at L2",
		"fixture-check.level.L2":           "fixture-check --level L2, read from exit 2",
		"fixture-check.level.L3":           "fixture-check --level L3, read from exit 2",
		"behaviour.l2-count-for-each":      "behaviour selects L2 for a for_each change",
		"behaviour.null-sides":             "behaviour gives an added attribute a null base",
		"flag:behaviour:base":              "behaviour --base",
		"flag:behaviour:diff":              "behaviour --diff",
		"flag:behaviour:schema":            "behaviour --schema",
		"flag:affected:base":               "affected --base",
		"flag:affected:diff":               "affected --diff",
		"flag:lint:schema-handoff":         "lint --schema-handoff, retried without on exit 2",
		"flag:lint:ai":                     "lint --ai",
		"cmd:claims-eval":                  "claims-eval",
		"cmd:depth":                        "depth",
		"cmd:rules":                        "rules",
		"flag:version:json":                "this report",
	}
	got := decodeVersionReport(t).Features
	for name, probe := range probes {
		if !slices.Contains(got, name) {
			t.Errorf("feature %q for the probe %q is missing", name, probe)
		}
	}
}

// TestReportVersions checks each schema_version the report lists equals
// the one its schema under schema/ accepts.
func TestReportVersions(t *testing.T) {
	files := map[string]string{
		"affected":      "affected.schema.json",
		"behaviour":     "behaviour.schema.json",
		"bundle":        "facts-bundle.schema.json",
		"claims-eval":   "claims-eval.schema.json",
		"fixture-check": "fixture-check.schema.json",
		"lint":          "envelope.schema.json",
		"schema-diff":   "schema-diff.schema.json",
	}
	r := decodeVersionReport(t)
	if len(r.Reports) != len(files) {
		t.Errorf("reports %v, want one entry per name in %v", r.Reports, files)
	}
	for name, file := range files {
		var s struct {
			Properties struct {
				SchemaVersion struct {
					Const *int  `json:"const"`
					Enum  []int `json:"enum"`
				} `json:"schema_version"`
			} `json:"properties"`
		}
		readJSON(t, filepath.Join("../../schema", file), &s)
		sv := s.Properties.SchemaVersion
		want := sv.Enum
		if sv.Const != nil {
			want = []int{*sv.Const}
		}
		if got, ok := r.Reports[name]; !ok || len(want) != 1 || got != want[0] {
			t.Errorf("%s: report says %d (present %v), %s accepts %v", name, got, ok, file, want)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
