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

	"github.com/spf13/cobra"
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
		"cmd:fixture-build":                "fixture-build in place of the ladder pass's own fixture builder",
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
		"flag:affected:module-root":        "affected --module-root, in place of the shell scan of repository-root examples",
		"depth.module-root":                "depth given a module root affected report",
		"flag:lint:schema-handoff":         "lint --schema-handoff, retried without on exit 2",
		"flag:lint:ai":                     "lint --ai",
		"lint.in-repo-downgrade":           "a relaxing profile is refused without in_repo_downgrade in the lint report",
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

// TestReportVersions runs each command the report lists on a fixture and
// checks the schema_version it writes equals the report's entry, and that
// the schema under schema/ accepts that value.
func TestReportVersions(t *testing.T) {
	const ex = "../../schema/examples"
	shas := []string{"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)}
	ftd := "../../testdata/fixture-check"
	sd := "../../testdata/schema-diff"
	statsFile := filepath.Join(t.TempDir(), "stats.json")
	cases := map[string]struct {
		args     []string
		envelope bool   // the version is .envelope.schema_version
		file     string // the report is this file, not stdout
		schema   string
	}{
		"stats":           {[]string{"lint", "../../testdata/variable-untyped-base/head", "--json", "--stats-file", statsFile}, false, statsFile, "stats.schema.json"},
		"affected":        {[]string{"affected", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--json"}, false, "", "affected.schema.json"},
		"changes":         {[]string{"changes", "../../testdata/changes/heredoc/head", "--base", "../../testdata/changes/heredoc/base", "--json"}, false, "", "changes.schema.json"},
		"behaviour":       {[]string{"behaviour", pr556 + "/head", "--base", pr556 + "/base", "--json"}, false, "", "behaviour.schema.json"},
		"claims-eval":     {append([]string{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json"}, shas...), false, "", "claims-eval.schema.json"},
		"depth":           {[]string{"depth", "--head", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--files", "../../testdata/diff-base/files.json"}, false, "", "depth.schema.json"},
		"rules":           {[]string{"rules", "--json"}, false, "", "rules.schema.json"},
		"fixture-build":   {fixtureBuildArgs(t, "ladder-pass-01"), false, "", "fixture-build.schema.json"},
		"fixture-check":   {[]string{"fixture-check", filepath.Join(ftd, "l1"), "--json"}, false, "", "fixture-check.schema.json"},
		"iam-policy-docs": {[]string{"iam-policy-docs", "../../testdata/iam-policy-docs/merge-local/head", "--base", "../../testdata/iam-policy-docs/merge-local/base"}, false, "", "iam-policy-docs.schema.json"},
		"lint":            {[]string{"lint", "../../testdata/variable-untyped-base/head", "--json"}, true, "", "envelope.schema.json"},
		"schema-diff":     {[]string{"schema-diff", "--from", filepath.Join(sd, "from.json"), "--to", filepath.Join(sd, "to.json"), "--types-from", filepath.Join(sd, "module")}, false, "", "schema-diff.schema.json"},
	}
	r := decodeVersionReport(t)
	for name := range r.Reports {
		if _, ok := cases[name]; !ok {
			t.Errorf("report %q has no case here: add one that runs it", name)
		}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := r.Reports[name]
			if !ok {
				t.Fatalf("version --json lists no %s", name)
			}
			_, out, errOut := runCLI(c.args)
			if c.file != "" {
				b, err := os.ReadFile(c.file)
				if err != nil {
					t.Fatalf("%v: %v, stderr %q", c.args, err, errOut)
				}
				out = string(b)
			}
			var doc struct {
				SchemaVersion *int `json:"schema_version"`
				Envelope      struct {
					SchemaVersion *int `json:"schema_version"`
				} `json:"envelope"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("%v: %v, stderr %q", c.args, err, errOut)
			}
			written := doc.SchemaVersion
			if c.envelope {
				written = doc.Envelope.SchemaVersion
			}
			if written == nil {
				t.Errorf("%s writes no schema_version, version --json says %d", name, got)
			} else if *written != got {
				t.Errorf("%s writes schema_version %d, version --json says %d", name, *written, got)
			}
			var s struct {
				Properties struct {
					SchemaVersion struct {
						Const *int  `json:"const"`
						Enum  []int `json:"enum"`
					} `json:"schema_version"`
				} `json:"properties"`
			}
			readJSON(t, filepath.Join("../../schema", c.schema), &s)
			sv := s.Properties.SchemaVersion
			want := sv.Enum
			if sv.Const != nil {
				want = []int{*sv.Const}
			}
			if len(want) != 1 || got != want[0] {
				t.Errorf("version --json says %d, %s accepts %v", got, c.schema, want)
			}
		})
	}
}

// TestCommandFeatures walks a synthetic tree: a hidden command, a
// deprecated command, a hidden and a deprecated flag, a persistent flag
// inherited two levels down, the root's own flags and help are left out.
func TestCommandFeatures(t *testing.T) {
	noop := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "tool", RunE: noop}
	root.Flags().Bool("version", false, "")
	root.PersistentFlags().String("trace", "", "")
	a := &cobra.Command{Use: "a", RunE: noop}
	a.Flags().String("x", "", "")
	a.Flags().String("old", "", "")
	if err := a.Flags().MarkDeprecated("old", "gone"); err != nil {
		t.Fatal(err)
	}
	a.Flags().String("secret", "", "")
	if err := a.Flags().MarkHidden("secret"); err != nil {
		t.Fatal(err)
	}
	a.PersistentFlags().Bool("p", false, "")
	b := &cobra.Command{Use: "b", RunE: noop}
	b.Flags().Int("n", 0, "")
	a.AddCommand(b)
	hidden := &cobra.Command{Use: "h", Hidden: true, RunE: noop}
	hidden.Flags().Bool("z", false, "")
	gone := &cobra.Command{Use: "d", Deprecated: "use a", RunE: noop}
	root.AddCommand(a, hidden, gone)
	root.InitDefaultHelpFlag()
	root.InitDefaultHelpCmd()
	a.InitDefaultHelpFlag()
	b.InitDefaultHelpFlag()

	got := commandFeatures(root)
	slices.Sort(got)
	want := []string{"cmd:a", "cmd:a.b", "flag:a.b:n", "flag:a.b:p", "flag:a.b:trace", "flag:a:p", "flag:a:trace", "flag:a:x"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
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
