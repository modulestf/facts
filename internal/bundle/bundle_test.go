package bundle

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

var update = flag.Bool("update", false, "rewrite testdata/bundle/expected.json")

const (
	testSkills = "../../testdata/bundle/skills"
	headSHA    = "1111111111111111111111111111111111111111"
	baseSHA    = "2222222222222222222222222222222222222222"
)

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	data, err := ReadRegistry(filepath.Join(testSkills, "references", "rule-facts.md"))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func testEngine(t *testing.T) EngineText {
	t.Helper()
	text, err := LoadEngine(filepath.Join(testSkills, "references", "engine"))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// testReport is a report with one finding of each tool rule, two candidates,
// a not_evaluated entry and an interface diff, over a change that adds,
// modifies and removes a file.
func testReport() *Report {
	return &Report{
		Envelope: facts.Envelope{
			SchemaVersion: facts.SchemaVersion,
			Tool:          facts.Tool{Name: "mtf-facts", Version: "v0.0.0", BinarySHA256: strings.Repeat("a", 64)},
			HeadSHA:       new(headSHA), MergeBaseSHA: new(baseSHA), RegistrySHA256: new(strings.Repeat("b", 64)),
			Inputs: facts.Inputs{
				Head: facts.TreeInput{Files: []facts.FileInput{
					{Path: "main.tf", Kind: "file", SHA256: strings.Repeat("1", 64)},
					{Path: "variables.tf", Kind: "file", SHA256: strings.Repeat("2", 64)},
					{Path: "examples/complete/main.tf", Kind: "file", SHA256: strings.Repeat("3", 64)},
				}},
				Base: &facts.TreeInput{Files: []facts.FileInput{
					{Path: "main.tf", Kind: "file", SHA256: strings.Repeat("1", 64)},
					{Path: "variables.tf", Kind: "file", SHA256: strings.Repeat("9", 64)},
					{Path: "outputs.tf", Kind: "file", SHA256: strings.Repeat("8", 64)},
				}},
			},
		},
		Manifest: []facts.ManifestEntry{
			{RuleID: "compat.default-changed", Area: ".", Status: facts.StatusClean},
			{RuleID: "compat.variable-removed", Area: ".", Status: facts.StatusCandidate},
			{RuleID: "coverage.unknown-argument", Area: ".", Status: facts.StatusClean},
			{RuleID: "examples.versions-missing", Area: "examples/complete", Status: facts.StatusFinding},
			{RuleID: "structure.missing-description", Area: ".", Status: facts.StatusFinding},
			{RuleID: "structure.variable-untyped", Area: ".", Status: facts.StatusNotEvaluated, Reason: "parse error", ReasonCode: facts.ReasonParseError},
		},
		Findings: []facts.Finding{
			{ID: "F-001", RuleID: "examples.versions-missing", Severity: "MEDIUM", File: "examples/complete", Summary: "No versions.tf.", SuggestedFix: "Add one."},
			{ID: "F-002", RuleID: "structure.missing-description", Severity: "MEDIUM", File: "variables.tf", Line: new(3), Summary: "No description.", SuggestedFix: "Add one."},
		},
		Candidates: []facts.Candidate{
			{CandidateID: "C-001", RuleID: "compat.variable-removed", File: "variables.tf", Subject: ".:var.retention_days",
				Evidence: facts.Evidence{SourceLines: []facts.SourceLine{{Revision: "merge_base", File: "variables.tf", Line: 1, Text: `variable "retention_days" {`}},
					Base: new(`variable "retention_days" {}`), SchemaFacts: []facts.SchemaFact{}}, EvidenceComplete: true},
		},
		Index:         []facts.IndexEntry{{File: "variables.tf", StartLine: 1, EndLine: 5, Subject: ".:var.name"}},
		InterfaceDiff: json.RawMessage(`{"head_sha":"` + headSHA + `","merge_base_sha":"` + baseSHA + `","areas":[{"path":".","kind":"root","required_version":null,"variables":[{"base":{"default":null,"description":null,"nullable":true,"sensitive":false,"type":null,"validation":[]},"change":"removed","head":null,"name":"retention_days"}],"outputs":[],"providers":[],"resources":[],"moved":[],"module_calls":[]}]}`),
	}
}

var big = Options{CapBytes: 1 << 30, InjectionBudget: 1 << 30}

func build(t *testing.T, r *Report, o Options) *Result {
	t.Helper()
	res, err := Build(r, testRegistry(t), testEngine(t), o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// parseRegion checks the region's shape and returns its payload and the
// bundle the payload holds.
func parseRegion(t *testing.T, b []byte) ([]byte, map[string]json.RawMessage) {
	t.Helper()
	lines := strings.Split(string(b), "\n")
	if len(lines) != 5 || lines[4] != "" || lines[1] != regionBegin || lines[3] != regionEnd {
		t.Fatalf("region is not header, begin, payload, end: %q", b)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(lines[0], headerPrefix))
	if err != nil || !strings.HasPrefix(lines[0], headerPrefix) || n != len(lines[2]) {
		t.Fatalf("header %q does not count the %d payload bytes", lines[0], len(lines[2]))
	}
	payload := []byte(lines[2])
	for _, c := range payload {
		if c < 0x20 || c > 0x7e {
			t.Fatalf("payload byte %#x is not printable ASCII", c)
		}
	}
	var s string
	if err := json.Unmarshal(payload, &s); err != nil {
		t.Fatal(err)
	}
	var d map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatal(err)
	}
	return payload, d
}

// keys returns the top-level keys of the bundle in file order.
func keys(t *testing.T, payload []byte) []string {
	t.Helper()
	var s string
	if err := json.Unmarshal(payload, &s); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	if _, err := dec.Token(); err != nil { // {
		t.Fatal(err)
	}
	var out []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestBuildJudgment(t *testing.T) {
	res := build(t, testReport(), big)
	if res.Mode != ModeJudgment || len(res.Shed) != 0 {
		t.Fatalf("mode %s, shed %v, reason %q", res.Mode, res.Shed, res.Reason)
	}
	payload, d := parseRegion(t, res.Bundle)
	want := []string{"tool_findings", "candidates", "area_rules", "interface_diff", "file_index", "severity_needed"}
	if got := keys(t, payload); !slices.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	if got := string(d["file_index"]); got != `[{"path":"examples/complete/main.tf","change":"added"},{"path":"outputs.tf","change":"removed"},{"path":"variables.tf","change":"modified"}]` {
		t.Errorf("file_index %s", got)
	}
	var tf []map[string]any
	if err := json.Unmarshal(d["tool_findings"], &tf); err != nil {
		t.Fatal(err)
	}
	if tf[0]["subject"] != "examples/complete" || tf[1]["subject"] != ".:var.name" || tf[0]["source"] != "tool" {
		t.Errorf("tool finding subjects %v, %v", tf[0], tf[1])
	}
	// The report has no schema coverage, so the rule that relies on it is
	// not_evaluated and injected, with the one parse_error entry.
	ar := string(d["area_rules"])
	for _, w := range []string{
		`{"rule_id":"coverage.unknown-argument","status":"not_evaluated","reason":"section_unavailable"}`,
		`{"rule_id":"structure.variable-untyped","status":"not_evaluated","reason":"parse_error"}`,
		`{"rule_id":"compat.variable-removed","status":"candidate"}`,
	} {
		if !strings.Contains(ar, w) {
			t.Errorf("area_rules %s lacks %s", ar, w)
		}
	}
	if strings.Contains(ar, "compat.default-changed") {
		t.Errorf("area_rules lists a clean rule: %s", ar)
	}
	inj := string(res.Injection)
	for _, w := range []string{"Rule `coverage.unknown-argument` is not_evaluated in:\n- area \".\", reason section_unavailable\n",
		"**Fails when:** an argument is not in the provider schema.", "**Fails when:** a new variable has no type."} {
		if !strings.Contains(inj, w) {
			t.Errorf("injection lacks %q:\n%s", w, inj)
		}
	}
	if strings.Contains(inj, "a default changes") {
		t.Errorf("injection holds a rule that was evaluated:\n%s", inj)
	}
}

// TestGolden pins the decoded bundle of testReport. The file validates
// against schema/facts-bundle.schema.json, with ajv as tests/schemas-test.sh
// runs it.
func TestGolden(t *testing.T) {
	_, d := parseRegion(t, build(t, testReport(), big).Bundle)
	got, err := json.MarshalIndent(orderedDoc(t, d), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := "../../testdata/bundle/expected.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("bundle differs from %s; run go test -update:\n%s", golden, got)
	}
}

// orderedDoc re-decodes the bundle into the builder's own type, which keeps
// the schema's key order.
func orderedDoc(t *testing.T, d map[string]json.RawMessage) any {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		ToolFindings   json.RawMessage `json:"tool_findings"`
		Candidates     json.RawMessage `json:"candidates"`
		AreaRules      json.RawMessage `json:"area_rules,omitempty"`
		InterfaceDiff  json.RawMessage `json:"interface_diff,omitempty"`
		SchemaCoverage json.RawMessage `json:"schema_coverage,omitempty"`
		FileIndex      json.RawMessage `json:"file_index,omitempty"`
		SeverityNeeded json.RawMessage `json:"severity_needed"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildIsDeterministic(t *testing.T) {
	a, b := build(t, testReport(), big), build(t, testReport(), big)
	if !bytes.Equal(a.Bundle, b.Bundle) || !bytes.Equal(a.Injection, b.Injection) {
		t.Error("two builds of one report differ")
	}
}

// payloadLen is the payload size of a build with no cap.
func payloadLen(t *testing.T, r *Report) int {
	t.Helper()
	p, _ := parseRegion(t, build(t, r, big).Bundle)
	return len(p)
}

func TestCapExactByte(t *testing.T) {
	n := payloadLen(t, testReport())
	at := build(t, testReport(), Options{CapBytes: n, InjectionBudget: big.InjectionBudget})
	if at.Mode != ModeJudgment || len(at.Shed) != 0 {
		t.Errorf("cap %d = payload: mode %s, shed %v", n, at.Mode, at.Shed)
	}
	if p, _ := parseRegion(t, at.Bundle); len(p) != n {
		t.Errorf("payload %d bytes, want %d", len(p), n)
	}
	under := build(t, testReport(), Options{CapBytes: n - 1, InjectionBudget: big.InjectionBudget})
	if !slices.Equal(under.Shed, []string{registry.SectionFileIndex}) {
		t.Errorf("cap %d: shed %v, want file-index", n-1, under.Shed)
	}
	if p, _ := parseRegion(t, under.Bundle); len(p) > n-1 {
		t.Errorf("payload %d bytes over cap %d", len(p), n-1)
	}
}

func TestShedOrder(t *testing.T) {
	r := testReport()
	full := payloadLen(t, r)
	type step struct {
		shed    []string
		missing []string // sections the bundle no longer has
	}
	steps := []step{
		{[]string{"file-index"}, []string{"file_index"}},
		{[]string{"file-index", "interface-diff"}, []string{"file_index", "interface_diff"}},
		{[]string{"file-index", "interface-diff", "area-rules"}, []string{"file_index", "interface_diff", "area_rules"}},
	}
	prev := full
	for _, s := range steps {
		// The largest cap that sheds exactly these sections: one byte under
		// the payload of the build that sheds one fewer.
		res := build(t, r, Options{CapBytes: prev - 1, InjectionBudget: big.InjectionBudget})
		if !slices.Equal(res.Shed, s.shed) || res.Mode != ModeJudgment {
			t.Fatalf("cap %d: mode %s, shed %v, want %v", prev-1, res.Mode, res.Shed, s.shed)
		}
		p, d := parseRegion(t, res.Bundle)
		for _, k := range s.missing {
			if _, ok := d[k]; ok {
				t.Errorf("shed %v: bundle still has %s", s.shed, k)
			}
		}
		for _, k := range []string{"tool_findings", "candidates", "severity_needed"} {
			if _, ok := d[k]; !ok {
				t.Errorf("shed %v: bundle lost %s", s.shed, k)
			}
		}
		prev = len(p)
	}
	// Shedding interface-diff makes the rules that rely on it not_evaluated.
	res := build(t, r, Options{CapBytes: payloadAfter(t, r, 1) - 1, InjectionBudget: big.InjectionBudget})
	_, d := parseRegion(t, res.Bundle)
	for _, w := range []string{
		`{"rule_id":"compat.default-changed","status":"not_evaluated","reason":"section_shed"}`,
		`{"rule_id":"compat.variable-removed","status":"not_evaluated","reason":"section_shed"}`,
		`{"rule_id":"examples.versions-missing","status":"not_evaluated","reason":"section_shed"}`,
	} {
		if !strings.Contains(string(d["area_rules"]), w) {
			t.Errorf("area_rules %s lacks %s", d["area_rules"], w)
		}
	}
	if !strings.Contains(string(res.Injection), "Rule `compat.default-changed` is not_evaluated in:\n- area \".\", reason section_shed") {
		t.Errorf("injection lacks the shed rule:\n%s", res.Injection)
	}
	// Under the payload of tool findings and candidates alone: full.
	alone := payloadAfter(t, r, 3)
	if res := build(t, r, Options{CapBytes: alone - 1, InjectionBudget: big.InjectionBudget}); res.Mode != ModeFull || res.Bundle != nil {
		t.Errorf("cap under sections 1 and 2: mode %s", res.Mode)
	}
	if res := build(t, r, Options{CapBytes: alone, InjectionBudget: big.InjectionBudget}); res.Mode != ModeJudgment {
		t.Errorf("cap at sections 1 and 2: mode %s, reason %s", res.Mode, res.Reason)
	}
}

// payloadAfter is the payload size once the first k sections of shedOrder
// that are present are shed.
func payloadAfter(t *testing.T, r *Report, k int) int {
	t.Helper()
	n := payloadLen(t, r)
	for range k {
		res := build(t, r, Options{CapBytes: n - 1, InjectionBudget: big.InjectionBudget})
		p, _ := parseRegion(t, res.Bundle)
		n = len(p)
	}
	return n
}

func TestSectionsOneAndTwoOverCapGiveFull(t *testing.T) {
	r := testReport()
	long := strings.Repeat("x", 1000)
	for i := range 80 {
		r.Candidates = append(r.Candidates, facts.Candidate{CandidateID: "C-" + strconv.Itoa(100+i), RuleID: "compat.variable-removed",
			File: "variables.tf", Subject: ".:var.v" + strconv.Itoa(i),
			Evidence: facts.Evidence{SourceLines: []facts.SourceLine{}, Head: &long, SchemaFacts: []facts.SchemaFact{}}, EvidenceComplete: true})
	}
	res := build(t, r, Options{CapBytes: DefaultCapBytes, InjectionBudget: DefaultInjectionBudget})
	if res.Mode != ModeFull || res.Bundle != nil || res.Injection != nil || !strings.Contains(res.Reason, "over the 49152 byte cap") {
		t.Errorf("mode %s, reason %q", res.Mode, res.Reason)
	}
	if !slices.Equal(res.Shed, []string{"file-index", "interface-diff", "area-rules"}) {
		t.Errorf("shed %v before giving up", res.Shed)
	}
}

func TestInjectionBudget(t *testing.T) {
	n := len(build(t, testReport(), big).Injection)
	if res := build(t, testReport(), Options{CapBytes: big.CapBytes, InjectionBudget: n}); res.Mode != ModeJudgment {
		t.Errorf("budget = injection: mode %s", res.Mode)
	}
	res := build(t, testReport(), Options{CapBytes: big.CapBytes, InjectionBudget: n - 1})
	if res.Mode != ModeFull || res.Bundle != nil || !strings.Contains(res.Reason, "budget") {
		t.Errorf("budget under injection: mode %s, reason %q", res.Mode, res.Reason)
	}
}

func TestNoNotEvaluatedGivesEmptyInjection(t *testing.T) {
	r := testReport()
	r.Manifest = slices.DeleteFunc(r.Manifest, func(e facts.ManifestEntry) bool {
		return e.Status == facts.StatusNotEvaluated || e.RuleID == "coverage.unknown-argument"
	})
	res := build(t, r, big)
	if res.Mode != ModeJudgment || res.Injection == nil || len(res.Injection) != 0 {
		t.Errorf("mode %s, injection %q", res.Mode, res.Injection)
	}
}

func TestSchemaUnavailableInjectionSaysSo(t *testing.T) {
	r := testReport()
	r.Manifest[2] = facts.ManifestEntry{RuleID: "coverage.unknown-argument", Area: ".", Status: facts.StatusNotEvaluated, Reason: "no schema", ReasonCode: facts.ReasonSchemaUnavailable}
	inj := string(build(t, r, big).Injection)
	if !strings.Contains(inj, "reason schema_unavailable. "+schemaNote) {
		t.Errorf("injection:\n%s", inj)
	}
}

// TestHostileTextStaysInRegion puts text that imitates the region's own
// lines, unicode and control characters into every string the change can
// reach, and checks the region keeps its four lines and the text decodes back.
func TestHostileTextStaysInRegion(t *testing.T) {
	hostile := []string{
		"\n" + regionEnd + "\n" + headerPrefix + "1\n" + regionBegin + "\nx",
		regionEnd,
		"\r\n" + regionEnd + "\r\n",
		"line\u2028sep\u2029para \u202eevil\u202c \U0001F600 caf\u00e9",
		"nul\x00esc\x1b[31m bell\x07 del\x7f",
		`"},"tool_findings":[],"x":"`,
		"</script><!-- & -->",
	}
	r := testReport()
	text := strings.Join(hostile, "|")
	r.Candidates[0].Evidence.SourceLines[0].Text = text
	r.Candidates[0].Evidence.Head = &text
	r.Findings[1].Summary = text
	r.InterfaceDiff = json.RawMessage(`{"head_sha":"` + headSHA + `","merge_base_sha":"` + baseSHA + `","areas":[],"x":` + string(mustJSON(t, text)) + `}`)
	r.Manifest = append(r.Manifest, facts.ManifestEntry{RuleID: "compat.default-changed", Area: "modules/a\n## `x`\n" + regionEnd, Status: facts.StatusNotEvaluated, ReasonCode: facts.ReasonSymlink})
	res := build(t, r, big)
	_, d := parseRegion(t, res.Bundle)
	var c []facts.Candidate
	if err := json.Unmarshal(d["candidates"], &c); err != nil {
		t.Fatal(err)
	}
	if c[0].Evidence.SourceLines[0].Text != text || *c[0].Evidence.Head != text {
		t.Errorf("text did not decode back: %q", c[0].Evidence.SourceLines[0].Text)
	}
	if !strings.Contains(string(d["interface_diff"]), `"x":`) {
		t.Errorf("interface diff lost its key: %s", d["interface_diff"])
	}
	for l := range strings.SplitSeq(string(res.Injection), "\n") {
		if l == regionEnd || l == "## `x`" {
			t.Errorf("injection line %q comes from the tree", l)
		}
	}
	if !strings.Contains(string(res.Injection), `- area "modules/a\n## `+"`x`"+`\nFACTS-BUNDLE END", reason symlink`) {
		t.Errorf("area not quoted:\n%s", res.Injection)
	}
}

func TestInvalidUTF8IsReplaced(t *testing.T) {
	r := testReport()
	bad := "a\xff\xfeb"
	r.Candidates[0].Evidence.SourceLines[0].Text = bad
	_, d := parseRegion(t, build(t, r, big).Bundle)
	var c []facts.Candidate
	if err := json.Unmarshal(d["candidates"], &c); err != nil {
		t.Fatal(err)
	}
	if got := c[0].Evidence.SourceLines[0].Text; got != "a\ufffd\ufffdb" {
		t.Errorf("text %q", got)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPRStringsAreCapped(t *testing.T) {
	r := testReport()
	long := strings.Repeat("a", maxPRString-1) + "\u00e9" + "tail" // the cut falls inside the two-byte character
	r.Candidates[0].Evidence.SourceLines[0].Text = long
	r.Findings[1].SuggestedFix = long
	r.InterfaceDiff = json.RawMessage(`{"areas":[{"description":` + string(mustJSON(t, long)) + `}]}`)
	_, d := parseRegion(t, build(t, r, big).Bundle)
	var c []facts.Candidate
	if err := json.Unmarshal(d["candidates"], &c); err != nil {
		t.Fatal(err)
	}
	if got := c[0].Evidence.SourceLines[0].Text; got != strings.Repeat("a", maxPRString-1) || c[0].EvidenceComplete {
		t.Errorf("evidence %d bytes, complete %v", len(got), c[0].EvidenceComplete)
	}
	var tf []facts.Finding
	if err := json.Unmarshal(d["tool_findings"], &tf); err != nil {
		t.Fatal(err)
	}
	marked := strings.Repeat("a", maxPRString-1) + " [truncated: 6 bytes omitted]"
	if tf[1].SuggestedFix != marked {
		t.Errorf("suggested_fix %q", tf[1].SuggestedFix)
	}
	if !strings.Contains(string(d["interface_diff"]), `{"areas":[{"description":"`+strings.Repeat("a", maxPRString-1)+` [truncated: 6 bytes omitted]"}]}`) {
		t.Errorf("interface diff %s", d["interface_diff"])
	}
	// A string at the cap is kept whole.
	r = testReport()
	at := strings.Repeat("b", maxPRString)
	r.Candidates[0].Evidence.Base = &at
	_, d = parseRegion(t, build(t, r, big).Bundle)
	if err := json.Unmarshal(d["candidates"], &c); err != nil {
		t.Fatal(err)
	}
	if *c[0].Evidence.Base != at || !c[0].EvidenceComplete {
		t.Errorf("base at the cap was cut")
	}
}

func TestCapStringsKeepsStructure(t *testing.T) {
	in := `{"b":[1,2.5,true,null,{"c":"d","e":[]}],"a":{},"f":"g"}`
	out, err := capStrings(json.RawMessage(in))
	if err != nil || string(out) != in {
		t.Errorf("capStrings %s, %v", out, err)
	}
	for _, bad := range []string{`{"a":`, `{} {}`, ``} {
		if _, err := capStrings(json.RawMessage(bad)); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
}

func TestBuildRefusesReportOffRegistry(t *testing.T) {
	for name, mutate := range map[string]func(*Report){
		"unknown rule":       func(r *Report) { r.Manifest[0].RuleID = "compat.no-such-rule" },
		"llm rule":           func(r *Report) { r.Findings[0].RuleID = "compat.break-unexplained" },
		"no reason code":     func(r *Report) { r.Manifest[5].ReasonCode = "" },
		"bad reason code":    func(r *Report) { r.Manifest[5].ReasonCode = "Parse Error" },
		"repeated candidate": func(r *Report) { r.Candidates = append(r.Candidates, r.Candidates[0]) },
		"bad candidate id":   func(r *Report) { r.Candidates[0].CandidateID = "X-1" },
		"no base tree":       func(r *Report) { r.Envelope.Inputs.Base = nil },
	} {
		t.Run(name, func(t *testing.T) {
			r := testReport()
			mutate(r)
			if _, err := Build(r, testRegistry(t), testEngine(t), big); err == nil {
				t.Error("built")
			}
		})
	}
}

func TestBuildRefusesMissingEngineText(t *testing.T) {
	text := testEngine(t)
	delete(text, "coverage.unknown-argument")
	if _, err := Build(testReport(), testRegistry(t), text, big); err == nil || !strings.Contains(err.Error(), "no engine text") {
		t.Errorf("err %v", err)
	}
}
