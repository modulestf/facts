package merge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// registryEnv names a local copy of the reviewer skill's rule-facts.md, the
// one CI fetches at a pinned commit.
const registryEnv = "MTF_FACTS_REGISTRY"

// fixtureDir holds the merge fixtures, byte-identical copies of the ones the
// facts engine spec was written against.
const fixtureDir = "../../testdata/merge"

// skippedFixtures are fixtures of the confirm step, which is not part of the
// merge.
var skippedFixtures = []string{"confirm-rebuild-revert"}

// pinnedRegistry reads the registry CI pins. It skips without one, except in
// CI, where a skip would hide the check.
func pinnedRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	path := os.Getenv(registryEnv)
	if path == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G703: the path is the CI-set registry copy
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// withoutIDs returns each finding as JSON without its id, sorted, so two
// lists compare as multisets.
func withoutIDs(t *testing.T, fs []Finding) []string {
	t.Helper()
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		f.ID = ""
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

// checkOrder checks the ids run from F-001 in the Final Step sort.
func checkOrder(t *testing.T, got []Finding) {
	t.Helper()
	for i, f := range got {
		if want := fmt.Sprintf("F-%03d", i+1); f.ID != want {
			t.Errorf("finding %d has id %s, want %s", i, f.ID, want)
		}
		if i > 0 && finalCmp(&got[i-1], &f) > 0 {
			t.Errorf("%s sorts before %s", f.ID, got[i-1].ID)
		}
	}
}

func TestFinalFixtures(t *testing.T) {
	reg := pinnedRegistry(t)
	templates, err := ParseTemplates(readFixture(t, fixtureDir, "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(fixtureDir, "*", "expected-findings.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	ran := 0
	for _, exp := range dirs {
		dir := filepath.Dir(exp)
		name := filepath.Base(dir)
		if slices.Contains(skippedFixtures, name) {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) {
			b, err := ParseBundle(regionForm(t, readFixture(t, dir, "bundle.json")))
			if err != nil {
				t.Fatal(err)
			}
			var repair []byte
			if _, err := os.Stat(filepath.Join(dir, "model-output.repair.json")); err == nil {
				repair = readFixture(t, dir, "model-output.repair.json")
			}
			got, err := Final(Inputs{Registry: reg, Bundle: b, Templates: templates}, readFixture(t, dir, "model-output.json"), repair)
			if err != nil {
				t.Fatal(err)
			}
			var want []Finding
			if err := json.Unmarshal(readFixture(t, dir, "expected-findings.json"), &want); err != nil {
				t.Fatal(err)
			}
			if g, w := withoutIDs(t, got), withoutIDs(t, want); !slices.Equal(g, w) {
				t.Errorf("merged findings differ\ngot:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(w, "\n"))
			}
			checkOrder(t, got)
		})
	}
	if want := len(dirs) - len(skippedFixtures); ran != want {
		t.Errorf("ran %d fixtures, want %d", ran, want)
	}
}

// TestFinalPinnedOverlapsKnown checks the merge implements every row of the
// pinned overlap table.
func TestFinalPinnedOverlapsKnown(t *testing.T) {
	reg := pinnedRegistry(t)
	for _, o := range reg.Overlaps {
		if _, ok := knownOverlaps[[2]string{o.KeptCell, o.DroppedCell}]; !ok {
			t.Errorf("pair %d is not implemented", o.Pair)
		}
	}
}

const oneCandidate = `{"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"outputs.tf","line":3,"subject":".:output.arn",
  "evidence":{"source_lines":[],"base":null,"head":null,"schema_facts":[]},"evidence_complete":true}],
 "severity_needed":[]}`

func TestFinalReplyChoice(t *testing.T) {
	reg := testRegistry(t)
	b := testBundle(t, oneCandidate)
	in := Inputs{Registry: reg, Bundle: b}
	confirm := func(text string) []byte {
		return []byte(`{"candidate_decisions":[{"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH","summary":"` +
			text + `","suggested_fix":"f"}],"findings":[]}`)
	}
	tests := []struct {
		name          string
		first, repair []byte
		wantSummary   string // "" for a review.check-not-run
		wantErr       bool
	}{
		{name: "valid first ignores repair", first: confirm("first"), repair: confirm("repair"), wantSummary: "first"},
		{name: "valid first without repair", first: confirm("first"), wantSummary: "first"},
		{name: "invalid first takes repair", first: []byte(`{}`), repair: confirm("repair"), wantSummary: "repair"},
		{name: "invalid first without repair", first: []byte(`{}`), wantErr: true},
		{name: "empty repair keeps nothing", first: []byte(`{}`), repair: []byte{}},
		{name: "repair not JSON keeps nothing", first: []byte(`not json`), repair: []byte(`{"candidate_decisions":[`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Final(in, tt.first, tt.repair)
			if tt.wantErr {
				if err == nil {
					t.Fatal("no error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d findings", len(got))
			}
			switch tt.wantSummary {
			case "":
				if got[0].RuleID != ruleCheckNotRun || got[0].Source != SourceHost || got[0].Subject != "structure.output-no-try@.:output.arn" {
					t.Errorf("got %+v, want a host review.check-not-run", got[0])
				}
			default:
				if got[0].Summary != tt.wantSummary || got[0].Source != SourceLLM || got[0].ConfirmedBy != "lead" {
					t.Errorf("got %+v, want the %s answer", got[0], tt.wantSummary)
				}
			}
		})
	}
}

// TestFinalSalvage merges a still-invalid repair reply: its valid answers
// and findings stay, and only the candidates it answers invalidly or not at
// all become review.check-not-run or get the severity_needed fallback.
func TestFinalSalvage(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"outputs.tf","line":3,"subject":".:output.a","evidence":{"source_lines":[],"base":null,"head":null,"schema_facts":[]},"evidence_complete":true},
 {"candidate_id":"C-002","rule_id":"structure.output-no-try","file":"outputs.tf","line":4,"subject":".:output.b","evidence":{"source_lines":[],"base":null,"head":null,"schema_facts":[]},"evidence_complete":true},
 {"candidate_id":"C-003","rule_id":"structure.output-no-try","file":"outputs.tf","line":5,"subject":".:output.c","evidence":{"source_lines":[],"base":null,"head":null,"schema_facts":[]},"evidence_complete":true}],
 "severity_needed":[
 {"candidate_id":"C-004","rule_id":"compat.variable-removed","file":"variables.tf","line":null,"subject":".:var.x","severities":["CRITICAL","HIGH"],"confirmed_by":"small"},
 {"candidate_id":"C-005","rule_id":"compat.variable-removed","file":"variables.tf","line":null,"subject":".:var.y","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`)
	repair := `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"reject","reason":"ok"},
 {"candidate_id":"C-002","decision":"reject","reason":"one"},
 {"candidate_id":"C-002","decision":"reject","reason":"two"},
 {"candidate_id":"C-003","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f","source":"tool"},
 {"candidate_id":"C-004","decision":"confirm","reason":"r","severity":"HIGH"},
 {"candidate_id":"C-005","decision":"reject","reason":"no"},
 {"candidate_id":"C-999","decision":"reject","reason":"unknown"}],
 "findings":[
 {"id":"F-001","rule_id":"structure.grouping","severity":"LOW","file":"main.tf","line":1,"summary":"kept","suggested_fix":"f"},
 {"id":"F-002","rule_id":"structure.grouping","severity":"LOW","file":"main.tf","line":2,"summary":"bad","suggested_fix":"f","subject":"x"}],
 "extra":true}`
	in := Inputs{Registry: testRegistry(t), Bundle: b, Templates: map[string]Template{
		"compat.variable-removed": {Summary: "{subject} removed", SuggestedFix: "restore {subject}"},
	}}
	got, err := Final(in, []byte(`{}`), []byte(repair))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range got {
		lines = append(lines, f.RuleID+" "+f.Source+" "+f.Severity+" "+f.Subject+" "+f.Summary+" "+f.ConfirmedBy)
	}
	want := []string{
		"compat.variable-removed host CRITICAL .:var.y .:var.y removed mid",
		"compat.variable-removed tool HIGH .:var.x .:var.x removed small",
		"review.check-not-run host MEDIUM structure.output-no-try@.:output.b " + got[2].Summary + " ",
		"review.check-not-run host MEDIUM structure.output-no-try@.:output.c " + got[3].Summary + " ",
		"structure.grouping llm LOW  kept ",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestFinalRefusesBadInputs(t *testing.T) {
	reg := testRegistry(t)
	valid := []byte(`{"candidate_decisions":[],"findings":[]}`)
	tests := []struct {
		name    string
		bundle  string
		reg     string // registry text; "" for the test registry
		wantErr string // a part of the error, when the case names one
	}{
		{name: "rule not in the registry", bundle: `{"tool_findings":[],"candidates":[{"candidate_id":"C-001","rule_id":"structure.nope","file":"a.tf","line":1,"subject":".:x","evidence":{},"evidence_complete":true}],"severity_needed":[]}`},
		{name: "severity_needed severities differ", bundle: `{"tool_findings":[],"candidates":[],"severity_needed":[{"candidate_id":"C-001","rule_id":"compat.variable-removed","file":"a.tf","line":1,"subject":".:x","severities":["HIGH","MEDIUM"],"confirmed_by":"mid"}]}`},
		{name: "severity_needed rule without severities", bundle: `{"tool_findings":[],"candidates":[],"severity_needed":[{"candidate_id":"C-001","rule_id":"structure.never-emitted","file":"a.tf","line":1,"subject":".:x","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`, wantErr: "has no severities"},
		{name: "severity_needed without template", bundle: `{"tool_findings":[],"candidates":[],"severity_needed":[{"candidate_id":"C-001","rule_id":"compat.variable-removed","file":"a.tf","line":1,"subject":".:x","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`},
		{name: "unknown overlap row", bundle: oneCandidate, reg: testRegistryText(strings.Replace(testOverlap, "| 2 | The `compat.*` finding for the break |", "| 2 | `compat.default-changed` |", 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reg
			if tt.reg != "" {
				var err error
				if r, err = registry.Parse(tt.reg); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Final(Inputs{Registry: r, Bundle: testBundle(t, tt.bundle)}, valid, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseBundleRefuses(t *testing.T) {
	ev := `"evidence":{},"evidence_complete":true`
	tests := map[string]string{
		"not an object":       `[]`,
		"missing section":     `{"tool_findings":[],"candidates":[]}`,
		"null section":        `{"tool_findings":[],"candidates":null,"severity_needed":[]}`,
		"unknown section":     `{"tool_findings":[],"candidates":[],"severity_needed":[],"extra":1}`,
		"repeated id":         `{"tool_findings":[],"candidates":[{"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"a.tf","line":1,"subject":"s",` + ev + `}],"severity_needed":[{"candidate_id":"C-001","rule_id":"compat.variable-removed","file":"a.tf","line":1,"subject":"s","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`,
		"bad path":            `{"tool_findings":[],"candidates":[{"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"../a.tf","line":1,"subject":"s",` + ev + `}],"severity_needed":[]}`,
		"lead tool finding":   `{"tool_findings":[{"id":"F-001","rule_id":"structure.output-no-try","severity":"HIGH","file":"a.tf","line":1,"summary":"s","suggested_fix":"f","source":"tool","subject":"s","candidate_id":"C-001","confirmed_by":"lead"}],"candidates":[],"severity_needed":[]}`,
		"unpaired confirmer":  `{"tool_findings":[{"id":"F-001","rule_id":"structure.output-no-try","severity":"HIGH","file":"a.tf","line":1,"summary":"s","suggested_fix":"f","source":"tool","subject":"s","confirmed_by":"small"}],"candidates":[],"severity_needed":[]}`,
		"too large":           `{"tool_findings":[],"candidates":[],"severity_needed":[],"file_index":"` + strings.Repeat("x", MaxBundleBytes) + `"}`,
		"malformed candidate": `{"tool_findings":[],"candidates":[{"candidate_id":"C-1","rule_id":"structure.output-no-try","file":"a.tf","line":1,"subject":"s",` + ev + `}],"severity_needed":[]}`,
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBundle(regionForm(t, []byte(text))); err == nil {
				t.Error("no error")
			}
		})
	}
	// The region form itself: Decode refuses each of these.
	twoValues := `"{\"tool_findings\":[],\"candidates\":[],\"severity_needed\":[]} {}"`
	good := string(regionForm(t, []byte(`{"tool_findings":[],"candidates":[],"severity_needed":[]}`)))
	for name, text := range map[string]string{
		"plain JSON":      `{"tool_findings":[],"candidates":[],"severity_needed":[]}`,
		"wrong count":     strings.Replace(good, "FACTS-BUNDLE-BYTES ", "FACTS-BUNDLE-BYTES 1", 1),
		"no end marker":   strings.Replace(good, "FACTS-BUNDLE END\n", "", 1),
		"text after":      good + "more\n",
		"two JSON values": fmt.Sprintf("FACTS-BUNDLE-BYTES %d\nFACTS-BUNDLE BEGIN\n%s\nFACTS-BUNDLE END\n", len(twoValues), twoValues),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBundle([]byte(text)); err == nil {
				t.Error("no error")
			}
		})
	}
}

// TestFinalLeadFindings covers what the merge does to the lead's findings:
// the allowed rules, the severity cap and floor, and the subject a lead
// finding dedups by.
func TestFinalLeadFindings(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[
 {"id":"F-001","rule_id":"profile.nonstandard-config-file","severity":"HIGH","file":"override.tf","line":null,"summary":"tool","suggested_fix":"f","source":"tool","subject":"override.tf"},
 {"id":"F-002","rule_id":"examples.versions-missing","severity":"MEDIUM","file":"examples/a/main.tf","line":7,"summary":"tool","suggested_fix":"f","source":"tool","subject":"examples/a/main.tf"},
 {"id":"F-003","rule_id":"scope.cross","severity":"HIGH","file":"modules/m/main.tf","line":2,"summary":"tool","suggested_fix":"f","source":"tool","subject":"modules/m:aws_x.this.arg_not_written"}],
 "candidates":[],
 "area_rules":[{"area":".","rules":[{"rule_id":"profile.nonstandard-config-file","status":"not_evaluated","reason":"file_index_shed"},{"rule_id":"scope.cross","status":"not_evaluated","reason":"parse_error"}]},
  {"area":"examples/a","rules":[{"rule_id":"examples.versions-missing","status":"not_evaluated","reason":"parse_error"}]},
  {"area":"modules/m","rules":[{"rule_id":"structure.output-no-try","status":"finding"}]}],
 "severity_needed":[]}`)
	index := []facts.IndexEntry{
		{File: "main.tf", StartLine: 1, EndLine: 9, Subject: ".:aws_s3_bucket.this"},
		{File: "main.tf", StartLine: 3, EndLine: 3, Subject: ".:aws_s3_bucket.this.tags"},
		{File: "main.tf", StartLine: 3, EndLine: 3, Subject: ".:aws_s3_bucket.this.tags.inner"},
	}
	finding := func(rule, sev, file, line, summary string) string {
		return `{"id":"F-001","rule_id":"` + rule + `","severity":"` + sev + `","file":"` + file + `","line":` + line +
			`,"summary":"` + summary + `","suggested_fix":"f"}`
	}
	reply := `{"candidate_decisions":[],"findings":[` + strings.Join([]string{
		finding("profile.nonstandard-config-file", "HIGH", "override.tf", "null", "path rule restates the tool"),
		finding("profile.nonstandard-config-file", "LOW", "main.tf", "5", "injected in the root, raised"),
		finding("profile.nonstandard-config-file", "HIGH", "modules/m/main.tf", "5", "not injected there"),
		finding("examples.versions-missing", "MEDIUM", "examples/a/main.tf", "7", "path rule subject is the file"),
		finding("scope.cross", "HIGH", "modules/m/main.tf", "2", "no derived subject, tool location"),
		finding("scope.cross", "HIGH", "modules/m/main.tf", "3", "cross-area, injected under the root"),
		finding("structure.output-no-try", "HIGH", "modules/m/outputs.tf", "1", "finding status is no injection"),
		finding("structure.grouping", "CRITICAL", "main.tf", "3", "capped, inner subject"),
		finding("structure.grouping", "LOW", "main.tf", "3", "same derived subject"),
		finding("structure.grouping", "LOW", "main.tf", "4", "outer subject"),
		finding("structure.grouping", "LOW", "tests/a.tftest.hcl", "4", "no area, llm rule kept"),
		finding("compat.default-changed", "HIGH", "variables.tf", "1", "candidate rule never injected"),
		finding("review.tool-disputed", "MEDIUM", "override.tf", "null", "disputed, capped to LOW"),
	}, ",") + `]}`
	got, err := Final(Inputs{Registry: testRegistry(t), Bundle: b, Index: index}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Source+" "+f.Severity+" "+f.RuleID+" "+f.Summary)
	}
	slices.Sort(lines)
	want := []string{
		"llm HIGH profile.nonstandard-config-file injected in the root, raised",
		"llm HIGH scope.cross cross-area, injected under the root",
		"llm LOW review.tool-disputed disputed, capped to LOW",
		"llm LOW structure.grouping capped, inner subject",
		"llm LOW structure.grouping no area, llm rule kept",
		"llm LOW structure.grouping outer subject",
		"tool HIGH profile.nonstandard-config-file tool",
		"tool HIGH scope.cross tool",
		"tool MEDIUM examples.versions-missing tool",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range got {
		if f.Source == SourceLLM && f.Subject != "" {
			t.Errorf("lead finding %q carries the derived subject %q", f.Summary, f.Subject)
		}
	}
}

// TestFinalDedup covers step 1 merges and both step 2 pairs. Pair 2 is
// inert by the owner's decision of 2026-10-22: a title finding on the
// break's own line stays beside the break.
func TestFinalDedup(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"compat.default-changed","file":"variables.tf","line":10,"subject":".:var.enable_x","evidence":{},"evidence_complete":true},
 {"candidate_id":"C-002","rule_id":"compat.default-changed","file":"variables.tf","line":20,"subject":".:var.retention","evidence":{},"evidence_complete":true}],
 "severity_needed":[
 {"candidate_id":"C-003","rule_id":"compat.variable-removed","file":"variables.tf","line":30,"subject":".:var.gone","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`)
	index := []facts.IndexEntry{
		{File: "variables.tf", StartLine: 10, EndLine: 12, Subject: ".:var.enable_x"},
		{File: "variables.tf", StartLine: 20, EndLine: 22, Subject: ".:var.retention"},
	}
	finding := func(rule, sev, file, line, summary string) string {
		return `{"id":"F-001","rule_id":"` + rule + `","severity":"` + sev + `","file":"` + file + `","line":` + line +
			`,"summary":"` + summary + `","suggested_fix":"f"}`
	}
	reply := `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"default enable_x","suggested_fix":"f"},
 {"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"default retention","suggested_fix":"f"},
 {"candidate_id":"C-003","decision":"confirm","reason":"r","severity":"HIGH","summary":"removed","suggested_fix":"f"}],
 "findings":[` + strings.Join([]string{
		finding("structure.flag-polarity", "MEDIUM", "variables.tf", "11", "polarity enable_x, dropped by pair 1"),
		finding("structure.flag-polarity", "MEDIUM", "variables.tf", "21", "polarity on a non-flag subject, kept"),
		finding("profile.commit-type-mismatch", "MEDIUM", "variables.tf", "30", "title on the break line, kept: pair 2 is inert"),
		finding("profile.commit-type-mismatch", "LOW", ".", "null", "title elsewhere, kept"),
		finding("compat.claim-contradicts-analysis", "HIGH", ".", "null", "claim, never kept over a title"),
		finding("structure.grouping", "LOW", "main.tf", "40", "second in arrival"),
		finding("structure.grouping", "LOW", "main.tf", "40", "third in arrival"),
	}, ",") + `]}`
	got, err := Final(Inputs{Registry: testRegistry(t), Bundle: b, Index: index}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, f := range got {
		lines = append(lines, f.ID+" "+f.Severity+" "+f.RuleID+" "+f.Summary)
	}
	want := []string{
		"F-001 HIGH compat.claim-contradicts-analysis claim, never kept over a title",
		"F-002 HIGH compat.variable-removed removed",
		"F-003 MEDIUM compat.default-changed default enable_x",
		"F-004 MEDIUM compat.default-changed default retention",
		"F-005 MEDIUM structure.flag-polarity polarity on a non-flag subject, kept",
		"F-006 MEDIUM profile.commit-type-mismatch title on the break line, kept: pair 2 is inert",
		"F-007 LOW profile.commit-type-mismatch title elsewhere, kept",
		"F-008 LOW structure.grouping second in arrival",
	}
	if !slices.Equal(lines, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestMergeGroupCheapestConfirmer merges two findings of one rule and
// subject with different confirmers: the cheapest class stays, with the
// text of the first in the sort.
func TestMergeGroupCheapestConfirmer(t *testing.T) {
	g := []*item{
		{f: Finding{RuleID: "r.a", Severity: "HIGH", File: "a.tf", Summary: "lead text", Source: SourceLLM, CandidateID: "C-002", ConfirmedBy: "lead"}, arrival: 2},
		{f: Finding{RuleID: "r.a", Severity: "MEDIUM", File: "a.tf", Summary: "mid text", Source: SourceTool, CandidateID: "C-001", ConfirmedBy: "mid"}, arrival: 1},
	}
	m := mergeGroup(g)
	if m.f.ConfirmedBy != "mid" || m.f.CandidateID != "C-001" || m.f.Source != SourceTool || m.f.Severity != "HIGH" || m.f.Summary != "lead text" {
		t.Errorf("got %+v", m.f)
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		sev       string
		permitted []string
		want      string
	}{
		{"CRITICAL", []string{"HIGH", "MEDIUM"}, "HIGH"},
		{"LOW", []string{"HIGH", "MEDIUM"}, "MEDIUM"},
		{"HIGH", []string{"MEDIUM", "CRITICAL"}, "MEDIUM"},
		{"MEDIUM", []string{"MEDIUM", "LOW"}, "MEDIUM"},
		{"CRITICAL", []string{"LOW"}, "LOW"},
	}
	for _, tt := range tests {
		t.Run(tt.sev+" in "+strings.Join(tt.permitted, ","), func(t *testing.T) {
			if got := clamp(tt.sev, tt.permitted); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestFindingArea(t *testing.T) {
	path := registry.Rule{Class: classPath}
	config := registry.Rule{Class: "config-semantic"}
	tests := []struct {
		name string
		rule registry.Rule
		file string
		want string // "" for no area
	}{
		{"root dot", config, ".", "."},
		{"root file", config, "main.tf", "."},
		{"github", config, ".github/workflows/x.yml", "."},
		{"submodule file", config, "modules/m/main.tf", "modules/m"},
		{"nested example file", config, "examples/a/b/c.tf", "examples/a"},
		{"loose example file", config, "examples/x.tf", ""},
		{"wrappers", config, "wrappers/main.tf", ""},
		{"test file", config, "x.tftest.hcl", ""},
		{"area directory, config rule", config, "examples/complete", ""},
		{"area directory, path rule", path, "examples/complete", "examples/complete"},
		{"submodule directory, path rule", path, "modules/m", "modules/m"},
		{"wrappers directory, path rule", path, "wrappers/x", "."},
		{"tests file, path rule", path, "tests/.checkov.yaml", "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findingArea(tt.rule, tt.file)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("findingArea(%q) = %q, %v; want %q", tt.file, got, ok, tt.want)
			}
		})
	}
}

func TestCheckMerged(t *testing.T) {
	good := Finding{ID: "F-001", RuleID: "structure.grouping", Severity: "LOW", File: "main.tf", Summary: "s", SuggestedFix: "f", Source: SourceLLM}
	tests := map[string]func(*Finding){
		"review critical":        func(f *Finding) { f.RuleID, f.Severity = "review.check-not-run", "CRITICAL" },
		"unknown source":         func(f *Finding) { f.Source = "model" },
		"tool without subject":   func(f *Finding) { f.Source = SourceTool },
		"tool confirmed by lead": func(f *Finding) { f.Source, f.Subject, f.CandidateID, f.ConfirmedBy = SourceTool, "s", "C-001", "lead" },
		"unpaired confirmer":     func(f *Finding) { f.ConfirmedBy = "lead" },
		"host check without @":   func(f *Finding) { f.Source, f.RuleID, f.Subject = SourceHost, ruleCheckNotRun, ".:x" },
		"host fallback no id":    func(f *Finding) { f.Source, f.Subject = SourceHost, ".:x" },
		"llm prefixed subject":   func(f *Finding) { f.Subject = "structure.grouping@.:x" },
		"bad path":               func(f *Finding) { f.File = "a\nb" },
		"line separator path":    func(f *Finding) { f.File = "a\u2028b" },
		"C1 control path":        func(f *Finding) { f.File = "a\u0085b" },
		"file subject elsewhere": func(f *Finding) { f.Source, f.Subject = SourceTool, "other.tf" },
		"check subject elsewhere": func(f *Finding) {
			f.Source, f.RuleID, f.Severity, f.Subject = SourceHost, ruleCheckNotRun, "MEDIUM", "structure.x@other.tf"
		},
	}
	if err := checkMerged(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			f := good
			mutate(&f)
			if err := checkMerged(f); err == nil {
				t.Error("no error")
			}
		})
	}
}

func TestParseTemplatesAndIndex(t *testing.T) {
	if _, err := ParseTemplates([]byte(`{"rules":{"structure.x":{"summary_template":"s"}}}`)); err == nil {
		t.Error("one template alone is accepted")
	}
	if _, err := ParseIndex([]byte(`{"index":[{"file":"../a","start_line":1,"end_line":1,"subject":"s"}]}`)); err == nil {
		t.Error("an index entry outside the module is accepted")
	}
	idx, err := ParseIndex([]byte(`{"envelope":{},"index":[{"file":"a.tf","start_line":1,"end_line":2,"subject":".:x"}]}`))
	if err != nil || len(idx) != 1 {
		t.Errorf("index %v, %v", idx, err)
	}
	if _, err := ParseIndex([]byte(`[`)); err == nil {
		t.Error("a broken report is accepted")
	}
}

// TestFinalCheckNotRunPerRule keeps two lead review.check-not-run findings
// for different rules in one file: review.* findings dedup by rule, file
// and line, never by the file as a path rule's subject.
func TestFinalCheckNotRunPerRule(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[],"candidates":[],"severity_needed":[]}`)
	reply := `{"candidate_decisions":[],"findings":[
 {"id":"F-001","rule_id":"review.check-not-run","severity":"MEDIUM","file":"main.tf","line":3,"summary":"rule A did not run","suggested_fix":"f"},
 {"id":"F-002","rule_id":"review.check-not-run","severity":"MEDIUM","file":"main.tf","line":9,"summary":"rule B did not run","suggested_fix":"f"},
 {"id":"F-003","rule_id":"review.tool-disputed","severity":"LOW","file":"main.tf","line":null,"summary":"dispute one","suggested_fix":"f"},
 {"id":"F-004","rule_id":"review.tool-disputed","severity":"LOW","file":"main.tf","line":2,"summary":"dispute two","suggested_fix":"f"}]}`
	got, err := Final(Inputs{Registry: testRegistry(t), Bundle: b}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Errorf("got %d findings, want 4: %+v", len(got), got)
	}
}

// TestFinalRefusesEmptySeverities refuses a bundle whose rule has no
// severities, so clamp never sees an empty list.
func TestFinalRefusesEmptySeverities(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"structure.never-emitted","file":"a.tf","line":1,"subject":".:x","evidence":{},"evidence_complete":true}],
 "severity_needed":[]}`)
	reply := `{"candidate_decisions":[{"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f"}],"findings":[]}`
	if _, err := Final(Inputs{Registry: testRegistry(t), Bundle: b}, []byte(reply), nil); err == nil {
		t.Error("no error")
	}
	if got := clamp("HIGH", nil); got != "HIGH" {
		t.Errorf("clamp on an empty list = %s", got)
	}
}

// TestFinalSeverityNeededOnce fails a merge that would lose a
// severity_needed entry, here to a tool finding on the same rule and
// subject.
func TestFinalSeverityNeededOnce(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[
 {"id":"F-001","rule_id":"compat.variable-removed","severity":"CRITICAL","file":"variables.tf","line":null,"summary":"s","suggested_fix":"f","source":"tool","subject":".:var.x","candidate_id":"C-001","confirmed_by":"small"}],
 "candidates":[],
 "severity_needed":[{"candidate_id":"C-002","rule_id":"compat.variable-removed","file":"variables.tf","line":null,"subject":".:var.x","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`)
	reply := `{"candidate_decisions":[{"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f"}],"findings":[]}`
	_, err := Final(Inputs{Registry: testRegistry(t), Bundle: b}, []byte(reply), nil)
	if err == nil || !strings.Contains(err.Error(), "C-002 is materialized 0 times") {
		t.Errorf("error %v", err)
	}
}
