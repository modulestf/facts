package merge

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const testDigest = "aed6203aa53a44b74647475ef4da2e39e654ee3b91220a2180c97000e40439c8"

// TestFinalBehaviourFields: a tool finding keeps predates_change, and a
// lead finding keeps explains and evidence_digest; two lead findings on one
// line with one digest merge into the union of their ids.
func TestFinalBehaviourFields(t *testing.T) {
	b := testBundle(t, `{"tool_findings":[
 {"id":"F-001","rule_id":"scope.cross","severity":"HIGH","file":"main.tf","line":2,"summary":"tool","suggested_fix":"f","source":"tool","subject":".:aws_x.this","predates_change":true}],
 "candidates":[],"area_rules":[],"severity_needed":[]}`)
	finding := func(explains, digest string) string {
		return `{"id":"F-001","rule_id":"compat.expression-behaviour-changed","severity":"LOW","file":"main.tf","line":10,` +
			`"summary":"s","suggested_fix":"f","explains":` + explains + `,"evidence_digest":"` + digest + `"}`
	}
	reply := `{"candidate_decisions":[],"findings":[` +
		finding(`["T-001|L1|b|value"]`, testDigest) + "," + finding(`["T-001|L1|a|value","T-001|L1|b|value"]`, testDigest) + `]}`
	got, err := Final(Inputs{Registry: testRegistry(t), Bundle: b}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 findings: %+v", got)
	}
	for _, f := range got {
		switch f.Source {
		case SourceTool:
			if !f.PredatesChange {
				t.Errorf("tool finding lost predates_change: %+v", f)
			}
		case SourceLLM:
			if !slices.Equal(f.Explains, []string{"T-001|L1|a|value", "T-001|L1|b|value"}) || f.EvidenceDigest != testDigest || f.PredatesChange {
				t.Errorf("lead finding: %+v", f)
			}
		}
		if err := checkMerged(f); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	out, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(out), `"predates_change":true`) || !strings.Contains(string(out), `"evidence_digest":"`+testDigest) {
		t.Errorf("JSON: %v %s", err, out)
	}

	// Two digests: the findings judged other evidence, so no union.
	reply = `{"candidate_decisions":[],"findings":[` +
		finding(`["T-001|L1|b|value"]`, testDigest) + "," + finding(`["T-001|L1|a|value"]`, strings.Repeat("0", 64)) + `]}`
	got, err = Final(Inputs{Registry: testRegistry(t), Bundle: b}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Source == SourceLLM && len(f.Explains) != 1 {
			t.Errorf("two digests merged their ids: %+v", f)
		}
	}
}

func TestParseFindingBehaviourFields(t *testing.T) {
	base := `"id":"F-001","severity":"LOW","file":"main.tf","line":10,"summary":"s","suggested_fix":"f"`
	many := make([]string, maxExplains+1)
	for i := range many {
		many[i] = `"T-001|L1|c` + strings.Repeat("x", i%60) + `|value"`
	}
	for name, c := range map[string]struct {
		extra string
		ok    bool
	}{
		"none":                  {`"rule_id":"structure.grouping"`, true},
		"behaviour rule":        {`"rule_id":"compat.expression-behaviour-changed","explains":["T-001|L1|a|value"],"evidence_digest":"` + testDigest + `"`, true},
		"claim rule":            {`"rule_id":"claim.refuted","explains":["T-001|L3|prior|action"]`, true},
		"other rule explains":   {`"rule_id":"structure.grouping","explains":["T-001|L1|a|value"]`, false},
		"other rule digest":     {`"rule_id":"structure.grouping","evidence_digest":"` + testDigest + `"`, false},
		"malformed id":          {`"rule_id":"claim.refuted","explains":["T-1|L1|a|value"]`, false},
		"empty explains":        {`"rule_id":"claim.refuted","explains":[]`, false},
		"too many ids":          {`"rule_id":"claim.refuted","explains":[` + strings.Join(many, ",") + `]`, false},
		"short digest":          {`"rule_id":"claim.refuted","evidence_digest":"abc"`, false},
		"upper-case digest":     {`"rule_id":"claim.refuted","evidence_digest":"` + strings.ToUpper(testDigest) + `"`, false},
		"model predates_change": {`"rule_id":"structure.grouping","predates_change":true`, false},
	} {
		e := parseFinding(json.RawMessage(`{` + base + `,` + c.extra + `}`))
		if e.ok != c.ok {
			t.Errorf("%s: ok %v, want %v", name, e.ok, c.ok)
		}
	}
}

func TestCheckMergedBehaviourFields(t *testing.T) {
	good := Finding{ID: "F-001", RuleID: "structure.grouping", Severity: "LOW", File: "main.tf", Summary: "s", SuggestedFix: "f", Source: SourceLLM}
	for name, mutate := range map[string]func(*Finding){
		"predates on an llm finding": func(f *Finding) { f.PredatesChange = true },
		"explains on another rule":   func(f *Finding) { f.Explains = []string{"T-001|L1|a|value"} },
		"digest on another rule":     func(f *Finding) { f.EvidenceDigest = testDigest },
	} {
		f := good
		mutate(&f)
		if err := checkMerged(f); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
