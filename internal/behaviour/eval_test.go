package behaviour

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

func pr556Report(t *testing.T) *Report {
	t.Helper()
	r, err := Run(Options{Head: filepath.Join(pr556, "head"), Base: filepath.Join(pr556, "base"), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func writeJSON(t *testing.T, name, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func claimsDoc(claims ...string) string {
	return `{"schema_version":1,"claims":[` + strings.Join(claims, ",") + `]}`
}

func TestClaimsValidation(t *testing.T) {
	b := pr556Report(t)
	cases := []struct {
		name, claim, why string
	}{
		{"valid unknown", `{"claim_id":"C-1","text":"compact makes it unknown","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":"main.tf","line":10}`, ""},
		{"valid length", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-length-known","params":{"node":"T-001","revision":"head","length":1},"trigger_id":"T-001","file":null,"line":null}`, ""},
		{"valid equals", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-equals-for-inputs","params":{"node":"T-001","revision":"head","inputs":{"vpc_security_group_ids":["sg-1"]},"expected":["sg-1"]},"trigger_id":null,"file":null,"line":null}`, ""},
		{"valid filtered", `{"claim_id":"C-1","text":"t","predicate_type":"input-filtered","params":{"node":"T-001","revision":"base","input":"vpc_security_group_ids","value":"empty-string"},"trigger_id":null,"file":null,"line":null}`, ""},
		{"valid action", `{"claim_id":"C-1","text":"t","predicate_type":"plan-action","params":{"node":"T-002","revision":"base","action":"no-op"},"trigger_id":null,"file":null,"line":null}`, ""},
		{"unknown predicate", `{"claim_id":"C-1","text":"t","predicate_type":"is-correct","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`, "closed predicate list"},
		{"free hcl node", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"compact(var.x)","revision":"base"},"trigger_id":null,"file":null,"line":null}`, "never by HCL text"},
		{"missing trigger", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-009","revision":"base"},"trigger_id":null,"file":null,"line":null}`, "not a trigger"},
		{"extra param", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base","expr":"x"},"trigger_id":null,"file":null,"line":null}`, "not allowed"},
		{"missing param", `{"claim_id":"C-1","text":"t","predicate_type":"plan-action","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`, `lacks "action"`},
		{"bad revision", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"both"},"trigger_id":null,"file":null,"line":null}`, "revision"},
		{"bad action", `{"claim_id":"C-1","text":"t","predicate_type":"plan-action","params":{"node":"T-001","revision":"base","action":"destroy"},"trigger_id":null,"file":null,"line":null}`, "params.action"},
		{"input not read", `{"claim_id":"C-1","text":"t","predicate_type":"input-filtered","params":{"node":"T-001","revision":"base","input":"tags","value":"null"},"trigger_id":null,"file":null,"line":null}`, "params.input"},
		{"equals input not read", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-equals-for-inputs","params":{"node":"T-001","revision":"head","inputs":{"tags":{}},"expected":[]},"trigger_id":null,"file":null,"line":null}`, "does not read"},
		{"negative length", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-length-known","params":{"node":"T-001","revision":"head","length":-1},"trigger_id":null,"file":null,"line":null}`, "length"},
		{"trigger mismatch", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":"T-002","file":null,"line":null}`, "differs"},
		{"unknown field", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null,"verdict":"pass"}`, "not a claim object"},
		{"bad id", `{"claim_id":"claim-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`, "claim_id"},
		{"empty text", `{"claim_id":"C-1","text":" ","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`, "text"},
		{"unsafe file", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":"../x.tf","line":null}`, "file"},
		{"zero line", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":0}`, "line"},
		{"params array", `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":[],"trigger_id":null,"file":null,"line":null}`, "params is not an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			valid, discarded, err := ReadClaims(writeJSON(t, "claims.json", claimsDoc(c.claim)), b)
			if err != nil {
				t.Fatal(err)
			}
			if c.why == "" {
				if len(valid) != 1 || len(discarded) != 0 {
					t.Errorf("want valid, got discarded %v", discarded)
				}
				return
			}
			if len(valid) != 0 || len(discarded) != 1 || !strings.Contains(discarded[0].Reason, c.why) {
				t.Errorf("want discarded with %q, got valid %d discarded %v", c.why, len(valid), discarded)
			}
		})
	}
	// A duplicate id discards the second claim only.
	one := `{"claim_id":"C-1","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`
	valid, discarded, err := ReadClaims(writeJSON(t, "c.json", claimsDoc(one, one)), b)
	if err != nil || len(valid) != 1 || len(discarded) != 1 || discarded[0].ClaimID != "claims[1]" {
		t.Errorf("duplicate: %v %v %v", err, valid, discarded)
	}
	for _, bad := range []string{`{"claims":[]}`, `{"schema_version":2,"claims":[]}`, `{"schema_version":1}`, `[]`, `{"schema_version":1,"claims":[]} {}`} {
		if _, _, err := ReadClaims(writeJSON(t, "c.json", bad), b); err == nil {
			t.Errorf("%s: want a file error", bad)
		}
	}
}

func TestClaimsCap(t *testing.T) {
	b := pr556Report(t)
	var claims []string
	for i := range MaxClaims + 3 {
		claims = append(claims, `{"claim_id":"C-`+itoa(i+1)+`","text":"t","predicate_type":"expression-output-unknown","params":{"node":"T-001","revision":"base"},"trigger_id":null,"file":null,"line":null}`)
	}
	valid, discarded, err := ReadClaims(writeJSON(t, "c.json", claimsDoc(claims...)), b)
	if err != nil || len(valid) != MaxClaims || len(discarded) != 3 {
		t.Errorf("cap: %v valid %d discarded %d", err, len(valid), len(discarded))
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// ladder builds a ladder record with the runs and coverage given as JSON.
func ladder(t *testing.T, runs, coverage string) *Ladder {
	t.Helper()
	doc := `{"schema_version":1,"head_sha":null,"base_sha":null,"terraform_version":"1.9.8","pins":[],"runs":[` + runs + `],"coverage":` + coverage + `}`
	l, err := ReadLadder(writeJSON(t, "ladder.json", doc))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const noCoverage = `{"class":"none","examples":[]}`

// L1 of PR 556: compact() is unknown for an unknown security group id;
// concat() is unknown too, but its length is known.
const l1Runs = `
{"trigger_id":"T-001","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{
  "base":[{"case":"unknown","known":false,"unknown_inputs":["aws_security_group.this"],"inputs":{"vpc_security_group_ids":[]}},
          {"case":"sample-1","known":true,"inputs":{"vpc_security_group_ids":["", "sg-1"]},"length":1,"value":["sg-1"]}],
  "head":[{"case":"unknown","known":false,"unknown_inputs":["aws_security_group.this"],"inputs":{"vpc_security_group_ids":[]},"length":1},
          {"case":"sample-1","known":true,"inputs":{"vpc_security_group_ids":["", "sg-1"]},"length":2,"value":["", "sg-1"]}]}},
{"trigger_id":"T-002","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{
  "base":[{"case":"unknown","known":false,"unknown_inputs":["aws_security_group.this"]}],
  "head":[{"case":"unknown","known":false,"unknown_inputs":["aws_security_group.this"],"length":1}]}}`

const l3Run = `
{"trigger_id":"T-002","level":"L3","state":"ran","reason":null,"fixture_sha256":"` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `","control":"valid","observations":{
  "base":[{"case":"prior","known":true,"action":"no-op","unchanged_vs_prior":true,"value":["sg-1"]}],
  "head":[{"case":"prior","known":false,"action":"update"}]}}`

func claim(id, pred, params string) string {
	return `{"claim_id":"` + id + `","text":"t","predicate_type":"` + pred + `","params":` + params + `,"trigger_id":null,"file":null,"line":null}`
}

func evalWith(t *testing.T, b *Report, l *Ladder, claims ...string) *EvalReport {
	t.Helper()
	valid, discarded, err := ReadClaims(writeJSON(t, "claims.json", claimsDoc(claims...)), b)
	if err != nil {
		t.Fatal(err)
	}
	return Evaluate(b, l, valid, discarded)
}

func states(r *EvalReport) (triggers, claims map[string]string) {
	triggers, claims = map[string]string{}, map[string]string{}
	for _, s := range r.Triggers {
		triggers[s.ID] = s.State
	}
	for _, s := range r.Claims {
		claims[s.ClaimID] = s.State
	}
	return triggers, claims
}

func TestEvaluatePR556(t *testing.T) {
	b := pr556Report(t)
	base := []string{
		claim("C-1", PredExprUnknown, `{"node":"T-001","revision":"base"}`),
		claim("C-2", PredExprLengthKnown, `{"node":"T-001","revision":"head","length":1}`),
		claim("C-3", PredExprUnknown, `{"node":"T-002","revision":"base"}`),
		claim("C-4", PredPlanAction, `{"node":"T-002","revision":"base","action":"no-op"}`),
		claim("C-5", PredPlanUnknown, `{"node":"T-002","revision":"head"}`),
		claim("C-6", PredInputFiltered, `{"node":"T-001","revision":"base","input":"vpc_security_group_ids","value":"empty-string"}`),
		claim("C-7", PredPlanUnchanged, `{"node":"T-002","revision":"base"}`),
	}

	// No ladder ran: everything is unverifiable, the P2 acceptance case.
	r := evalWith(t, b, ladder(t, "", noCoverage), base...)
	tr, cl := states(r)
	if tr["T-001"] != StateUnverifiable || tr["T-002"] != StateUnverifiable {
		t.Errorf("no ladder: triggers %v", tr)
	}
	for id, s := range cl {
		if s != StateUnverifiable {
			t.Errorf("no ladder: %s is %s", id, s)
		}
	}

	// L1 only: the claims decide themselves; the triggers miss their
	// required cases.
	r = evalWith(t, b, ladder(t, l1Runs, noCoverage), base...)
	tr, cl = states(r)
	if tr["T-001"] != StateUnverifiable || tr["T-002"] != StateUnverifiable || cl["C-1"] != StateVerified ||
		cl["C-2"] != StateVerified || cl["C-6"] != StateVerified || cl["C-4"] != StateUnverifiable {
		t.Errorf("L1: triggers %v claims %v\n%s", tr, cl, mustJSON(t, r))
	}
	if !strings.Contains(r.Triggers[0].Reason, "missing") {
		t.Errorf("L1: T-001 reason %q", r.Triggers[0].Reason)
	}

	// L1 and L3: the plan claims verify.
	r = evalWith(t, b, ladder(t, l1Runs+","+l3Run, noCoverage), base...)
	_, cl = states(r)
	if cl["C-4"] != StateVerified || cl["C-5"] != StateVerified || cl["C-7"] != StateVerified {
		t.Errorf("L1+L3: claims %v\n%s", cl, mustJSON(t, r))
	}
	if r.Claims[0].TriggerID != "T-001" || r.Claims[0].Revision != "base" {
		t.Errorf("claim echo: %+v", r.Claims[0])
	}

	// A refuted claim refutes its trigger.
	r = evalWith(t, b, ladder(t, l1Runs+","+l3Run, noCoverage), append(base,
		claim("C-8", PredPlanAction, `{"node":"T-002","revision":"head","action":"no-op"}`))...)
	tr, cl = states(r)
	if cl["C-8"] != StateRefuted || tr["T-002"] != StateRefuted {
		t.Errorf("refuted: triggers %v claims %v", tr, cl)
	}

	// An invalid control does not count.
	bad := strings.ReplaceAll(l1Runs, `"control":"valid"`, `"control":"invalid"`)
	_, cl = states(evalWith(t, b, ladder(t, bad, noCoverage), base...))
	if cl["C-1"] != StateUnverifiable {
		t.Errorf("invalid control: C-1 %s", cl["C-1"])
	}
}

// sweepRun is an L1 or L3 run of trigger id with the given base and head
// observations.
func sweepRun(id, level, base, head string) string {
	return `{"trigger_id":"` + id + `","level":"` + level + `","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{"base":[` +
		base + `],"head":[` + head + `]}}`
}

// The PR 556 sweep: an unknown security group id, and ids ["x", ""].
const (
	caseUnknownBase = `{"case":"unknown-element","known":false,"unknown_inputs":["aws_security_group.this[*].id"],"inputs":{"vpc_security_group_ids":[]}}`
	caseUnknownHead = `{"case":"unknown-element","known":false,"unknown_inputs":["aws_security_group.this[*].id"],"inputs":{"vpc_security_group_ids":[]},"length":1}`
	caseIDsBase     = `{"case":"ids","known":true,"inputs":{"vpc_security_group_ids":["x",""]},"length":1,"value":["x"]}`
	caseIDsHead     = `{"case":"ids","known":true,"inputs":{"vpc_security_group_ids":["x",""]},"length":2,"value":["x",""]}`
)

// withCases sets the required L1 cases of a trigger of b to ids.
func withCases(b *Report, id string, ids ...string) {
	for i := range b.Triggers {
		if b.Triggers[i].ID == id {
			b.Triggers[i].Cases = nil
			for _, c := range ids {
				b.Triggers[i].Cases = append(b.Triggers[i].Cases, Case{ID: c, Inputs: map[string]CaseInput{}})
			}
		}
	}
}

func TestSweep(t *testing.T) {
	b := pr556Report(t)
	withCases(b, "T-001", "unknown-element", "ids")
	run := sweepRun("T-001", "L1", caseUnknownBase+","+caseIDsBase, caseUnknownHead+","+caseIDsHead)
	unknown := []string{
		claim("C-1", PredExprUnknown, `{"node":"T-001","revision":"base"}`),
		claim("C-2", PredExprLengthKnown, `{"node":"T-001","revision":"head","length":1}`),
	}
	equals := []string{
		claim("C-3", PredExprEquals, `{"node":"T-001","revision":"base","inputs":{"vpc_security_group_ids":["x",""]},"expected":["x"]}`),
		claim("C-4", PredExprEquals, `{"node":"T-001","revision":"head","inputs":{"vpc_security_group_ids":["x",""]},"expected":["x",""]}`),
	}
	stateOf := func(r *EvalReport, id string) TriggerState {
		for _, s := range r.Triggers {
			if s.ID == id {
				return s
			}
		}
		return TriggerState{}
	}
	cases := []struct {
		name, runs string
		claims     []string
		want       string
		changes    int
		explained  int
	}{
		{"no difference needs no claim", sweepRun("T-001", "L1", caseIDsBase+","+caseUnknownBase, caseIDsBase+","+caseUnknownBase), nil, StateVerified, 0, 0},
		{"differences without claims", run, nil, StateUnverifiable, 3, 0},
		{"unknown case explained only", run, unknown, StateUnverifiable, 3, 1},
		{"every difference explained", run, append(slices.Clone(unknown), equals...), StateVerified, 3, 3},
		{"a case missing at the head", sweepRun("T-001", "L1", caseUnknownBase+","+caseIDsBase, caseUnknownHead), nil, StateUnverifiable, 0, 0},
		{"a required case missing at both", sweepRun("T-001", "L1", caseIDsBase, caseIDsBase), nil, StateUnverifiable, 0, 0},
		{"an extra case is ignored", sweepRun("T-001", "L1", caseIDsBase+","+caseUnknownBase+`,{"case":"extra","known":true,"value":1}`,
			caseIDsBase+","+caseUnknownBase+`,{"case":"extra","known":true,"value":2}`), nil, StateVerified, 0, 0},
		{"no head observations", sweepRun("T-001", "L1", caseIDsBase, ""), nil, StateUnverifiable, 0, 0},
		{"sensitive at both", sweepRun("T-001", "L1", caseIDsBase+`,{"case":"unknown-element","known":true,"sensitive":true}`, caseIDsBase+`,{"case":"unknown-element","known":true,"sensitive":true}`), nil, StateUnverifiable, 0, 0},
		{"unknown inputs differ", sweepRun("T-001", "L1", caseIDsBase+`,{"case":"unknown-element","known":false,"unknown_inputs":["a"]}`, caseIDsBase+`,{"case":"unknown-element","known":false,"unknown_inputs":["b"]}`), nil, StateUnverifiable, 1, 0},
		{"an invalid run", run + `,{"trigger_id":"T-001","level":"L1","state":"unverifiable","reason":"canary","fixture_sha256":null,"control":"invalid","observations":null}`,
			append(slices.Clone(unknown), equals...), StateUnverifiable, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := evalWith(t, b, ladder(t, c.runs, noCoverage), c.claims...)
			s := stateOf(r, "T-001")
			n := 0
			for _, ch := range s.Changes {
				if ch.Explained {
					n++
				}
			}
			if s.State != c.want || len(s.Changes) != c.changes || n != c.explained {
				t.Errorf("%s with %d changes, %d explained; want %s %d %d\n%s", s.State, len(s.Changes), n, c.want, c.changes, c.explained, mustJSON(t, r))
			}
		})
	}
	withCases(b, "T-001")
	r := evalWith(t, b, ladder(t, run, noCoverage))
	if s := stateOf(r, "T-001"); s.State != StateUnverifiable || !strings.Contains(s.Reason, "no required case") {
		t.Errorf("no required cases: %+v", s)
	}
}

// simulate is the L1 output of vpc_security_group_ids for one required case
// of the PR 556 sweep: concat at the head, compact(concat) at the base.
func simulate(c Case, compact bool) string {
	type elem struct {
		v       any
		unknown bool
	}
	var list []elem
	whole, fail := false, false
	for _, name := range []string{"aws_security_group.this[*].id", "vpc_security_group_ids"} {
		in := c.Inputs[name]
		switch {
		case in.Unknown:
			whole = true
		case in.Null:
			fail = true
		default:
			var vs []any
			_ = json.Unmarshal(in.Value, &vs)
			for i, v := range vs {
				list = append(list, elem{v: v, unknown: len(in.UnknownElements) > 0 && i == 0})
			}
		}
	}
	unknownElem := false
	for _, e := range list {
		unknownElem = unknownElem || e.unknown
	}
	if compact && unknownElem {
		whole = true
	}
	switch {
	case fail, whole:
		return `{"case":"` + c.ID + `","known":false}`
	case unknownElem:
		return `{"case":"` + c.ID + `","known":false,"length":` + itoa(len(list)) + `}`
	}
	out := []any{}
	for _, e := range list {
		if !compact || e.v != nil && e.v != "" {
			out = append(out, e.v)
		}
	}
	v, _ := json.Marshal(out)
	return `{"case":"` + c.ID + `","known":true,"length":` + itoa(len(out)) + `,"value":` + string(v) + `}`
}

// TestSweepPR556Required: one case is not enough; the full required sweep,
// with the real compact and concat behaviour, lists the differences.
func TestSweepPR556Required(t *testing.T) {
	b := pr556Report(t)
	cases := b.Triggers[0].Cases
	one := sweepRun("T-001", "L1", simulate(cases[0], true), simulate(cases[0], false))
	r := evalWith(t, b, ladder(t, one, noCoverage))
	if s := r.Triggers[0]; s.State != StateUnverifiable || !strings.Contains(s.Reason, "missing") {
		t.Errorf("one case: %s", mustJSON(t, s))
	}
	var base, head []string
	for _, c := range cases {
		base, head = append(base, simulate(c, true)), append(head, simulate(c, false))
	}
	r = evalWith(t, b, ladder(t, sweepRun("T-001", "L1", strings.Join(base, ","), strings.Join(head, ",")), noCoverage))
	s := r.Triggers[0]
	if s.State != StateUnverifiable || len(s.Changes) == 0 {
		t.Fatalf("full sweep: %s", mustJSON(t, s))
	}
	found := false
	for _, c := range s.Changes {
		if c.Case == "empty-element.empty" && c.Attribute == "value" && !c.Explained {
			found = true
		}
	}
	if !found {
		t.Errorf("no unexplained value change for an empty-string element: %s", mustJSON(t, s.Changes))
	}
}

// TestSweepL3 is the PR 556 replay: no change planned at base, known after
// apply at head, explained by the plan claims.
func TestSweepL3(t *testing.T) {
	b := pr556Report(t)
	withCases(b, "T-002", "unknown-element")
	l1 := sweepRun("T-002", "L1", caseUnknownBase, caseUnknownHead)
	l3 := sweepRun("T-002", "L3", `{"case":"prior","known":true,"action":"no-op","unchanged_vs_prior":true,"value":["sg-1"]}`,
		`{"case":"prior","known":false,"action":"update","unchanged_vs_prior":false}`)
	claims := []string{
		claim("C-1", PredExprUnknown, `{"node":"T-002","revision":"base"}`),
		claim("C-2", PredExprLengthKnown, `{"node":"T-002","revision":"head","length":1}`),
		claim("C-3", PredPlanUnchanged, `{"node":"T-002","revision":"base"}`),
		claim("C-4", PredPlanUnknown, `{"node":"T-002","revision":"head"}`),
		claim("C-5", PredPlanAction, `{"node":"T-002","revision":"base","action":"no-op"}`),
		claim("C-6", PredPlanAction, `{"node":"T-002","revision":"head","action":"update"}`),
	}
	r := evalWith(t, b, ladder(t, l1+","+l3, noCoverage), claims...)
	if s := r.Triggers[1]; s.ID != "T-002" || s.State != StateVerified || len(s.Changes) != 4 {
		t.Errorf("T-002: %s", mustJSON(t, r))
	}
	r = evalWith(t, b, ladder(t, l1+","+l3, noCoverage), claims[:5]...)
	if s := r.Triggers[1]; s.State != StateUnverifiable || !strings.Contains(s.Reason, "action") {
		t.Errorf("without the head action claim: %s", mustJSON(t, s))
	}
	// Only the L1 level ran: L3 is missing.
	r = evalWith(t, b, ladder(t, l1, noCoverage), claims...)
	if s := r.Triggers[1]; s.State != StateUnverifiable || !strings.Contains(s.Reason, "L3") {
		t.Errorf("no L3: %s", mustJSON(t, s))
	}
}

func TestEvaluateValues(t *testing.T) {
	b := pr556Report(t)
	runs := `{"trigger_id":"T-001","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{"base":[],"head":[
	  {"case":"a","known":true,"inputs":{"vpc_security_group_ids":["sg-1"]},"value":["sg-1"]},
	  {"case":"n","known":true,"inputs":{"vpc_security_group_ids":[1.0]},"value":[1]},
	  {"case":"s","known":true,"sensitive":true,"inputs":{"vpc_security_group_ids":["sg-2"]}},
	  {"case":"u","known":false,"inputs":{"vpc_security_group_ids":["sg-3"]}}]}}`
	l := ladder(t, runs, noCoverage)
	eq := func(id, in, want string) string {
		return claim(id, PredExprEquals, `{"node":"T-001","revision":"head","inputs":{"vpc_security_group_ids":`+in+`},"expected":`+want+`}`)
	}
	_, cl := states(evalWith(t, b, l,
		eq("C-1", `["sg-1"]`, `["sg-1"]`),
		eq("C-2", `["sg-1"]`, `[]`),
		eq("C-3", `[1]`, `[1.00]`),
		eq("C-4", `["sg-2"]`, `["sg-2"]`),
		eq("C-5", `["sg-3"]`, `["sg-3"]`),
		eq("C-6", `["sg-9"]`, `["sg-9"]`),
		eq("C-7", `[1]`, `["1"]`),
	))
	want := map[string]string{"C-1": StateVerified, "C-2": StateRefuted, "C-3": StateVerified, "C-4": StateUnverifiable,
		"C-5": StateUnverifiable, "C-6": StateUnverifiable, "C-7": StateRefuted}
	for id, s := range want {
		if cl[id] != s {
			t.Errorf("%s: %s, want %s", id, cl[id], s)
		}
	}
}

func TestEvaluateCovered(t *testing.T) {
	line, addr, attr := 3, "aws_s3_bucket.b", "bucket"
	b := &Report{SchemaVersion: 1, Entries: []Entry{{Path: "main.tf", ChangeKind: KindModified, TriggerIDs: []string{"T-001", "T-002", "T-003", "T-004"}},
		{Path: "README.md", ChangeKind: KindModified, Inert: true, TriggerIDs: []string{"T-005"}}},
		Triggers: []Trigger{
			{ID: "T-001", File: "main.tf", Line: &line, Category: CatLiteral, Address: &addr, Attribute: &attr, Levels: []string{LevelL4}},
			{ID: "T-002", File: "main.tf", Line: &line, Category: CatLiteral, Address: &addr, Attribute: new("tags"), Levels: []string{LevelL4}},
			{ID: "T-003", File: "main.tf", Line: &line, Category: CatWrapper, Address: &addr, Attribute: &attr, Levels: []string{LevelL1}},
			{ID: "T-004", File: "main.tf", Category: CatNonHCL, Levels: []string{LevelL4}},
			{ID: "T-005", File: "README.md", Category: CatNonHCL, Levels: []string{LevelL4}},
		}}
	cov := `{"class":"complete","examples":[
	  {"dir":"examples/complete","head":"planned","base":null,"addresses":["module.s3.aws_s3_bucket.b","aws_s3_bucket.b"],"attributes":{"module.s3.aws_s3_bucket.b":["bucket"]},"module_calls":{"s3":"."}},
	  {"dir":"examples/other","head":"code-error","base":null,"addresses":[],"attributes":{}}]}`
	r := evalWith(t, b, ladder(t, "", cov))
	tr, _ := states(r)
	want := map[string]string{"T-001": StateCovered, "T-002": StateUnverifiable, "T-003": StateUnverifiable,
		"T-004": StateUnverifiable, "T-005": StateInert}
	for id, s := range want {
		if tr[id] != s {
			t.Errorf("%s: %s, want %s\n%s", id, tr[id], s, mustJSON(t, r))
		}
	}
	// A call the record does not tie to the root module covers nothing.
	cov = `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,"addresses":["module.vpc.aws_s3_bucket.b"],"attributes":{"module.vpc.aws_s3_bucket.b":["bucket"]},"module_calls":{"s3":"."}}]}`
	if tr, _ := states(evalWith(t, b, ladder(t, "", cov))); tr["T-001"] != StateUnverifiable {
		t.Errorf("other module: T-001 %s", tr["T-001"])
	}
	cov = `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,"addresses":["module.s3.aws_s3_bucket.b"],"attributes":{"module.s3.aws_s3_bucket.b":["bucket"]}}]}`
	if tr, _ := states(evalWith(t, b, ladder(t, "", cov))); tr["T-001"] != StateUnverifiable {
		t.Errorf("no module_calls: T-001 %s", tr["T-001"])
	}
	// An example's own resource does not cover a module resource.
	cov = `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,"addresses":["aws_s3_bucket.b"],"attributes":{"aws_s3_bucket.b":["bucket"]}}]}`
	if tr, _ := states(evalWith(t, b, ladder(t, "", cov))); tr["T-001"] != StateUnverifiable {
		t.Errorf("example resource: T-001 %s", tr["T-001"])
	}
}

// TestEvaluateCoveredFailsClosed: a submodule trigger, a module call
// argument, an unverifiable claim or an invalid run never pass.
func TestEvaluateCoveredFailsClosed(t *testing.T) {
	line := 3
	b := &Report{SchemaVersion: 1, Entries: []Entry{}, Triggers: []Trigger{
		{ID: "T-001", File: "modules/x/main.tf", Line: &line, Category: CatLiteral, Address: new("aws_s3_bucket.b"), Attribute: new("bucket"), Levels: []string{LevelL4}},
		{ID: "T-002", File: "main.tf", Line: &line, Category: CatUnclassified, Address: new("module.m"), Attribute: new("name"), Levels: []string{LevelL4}},
		{ID: "T-003", File: "main.tf", Line: &line, Category: CatResourceAddRemove, Address: new("module.m"), Levels: []string{LevelL4}},
		{ID: "T-004", File: "main.tf", Line: &line, Category: CatLiteral, Address: new("aws_s3_bucket.b"), Attribute: new("bucket"), Levels: []string{LevelL4}},
		{ID: "T-005", File: "main.tf", Line: &line, Category: CatLiteral, Address: new("aws_s3_bucket.b"), Attribute: new("bucket"), Levels: []string{LevelL4}},
		{ID: "T-006", File: "main.tf", Line: &line, Category: CatWrapper, Address: new("local.x"), BaseExpr: new("a"), HeadExpr: new("b"), Levels: []string{LevelL1}},
	}}
	cov := `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,
	  "addresses":["module.s3.aws_s3_bucket.b","module.s3.module.x.aws_s3_bucket.b","module.s3.module.m.aws_s3_bucket.c"],
	  "attributes":{"module.s3.aws_s3_bucket.b":["bucket"],"module.s3.module.x.aws_s3_bucket.b":["bucket"],"module.s3.module.m.aws_s3_bucket.c":["name"]},
	  "module_calls":{"s3":"."}}]}`
	runs := `{"trigger_id":"T-006","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{"base":[{"case":"a","known":false,"unknown_inputs":["x"]}],"head":[]}},
	  {"trigger_id":"T-006","level":"L1","state":"unverifiable","reason":"canary","fixture_sha256":null,"control":"invalid","observations":null}`
	r := evalWith(t, b, ladder(t, runs, cov),
		claim("C-1", PredPlanAction, `{"node":"T-005","revision":"head","action":"update"}`),
		claim("C-2", PredExprUnknown, `{"node":"T-006","revision":"base"}`))
	tr, cl := states(r)
	want := map[string]string{"T-001": StateUnverifiable, "T-002": StateUnverifiable, "T-003": StateCovered,
		"T-004": StateCovered, "T-005": StateUnverifiable, "T-006": StateUnverifiable}
	for id, s := range want {
		if tr[id] != s {
			t.Errorf("%s: %s, want %s\n%s", id, tr[id], s, mustJSON(t, r))
		}
	}
	if cl["C-2"] != StateVerified {
		t.Errorf("C-2: %s", cl["C-2"])
	}
}

func TestReadLadderRefuses(t *testing.T) {
	good := `{"schema_version":1,"head_sha":null,"base_sha":null,"terraform_version":null,"pins":[],"runs":[],"coverage":{"class":"none","examples":[]}}`
	if _, err := ReadLadder(writeJSON(t, "l.json", good)); err != nil {
		t.Fatalf("the empty P2 record: %v", err)
	}
	for name, bad := range map[string]string{
		"extra field":   strings.Replace(good, `"pins"`, `"verdict":"pass","pins"`, 1),
		"bad sha":       strings.Replace(good, `"head_sha":null`, `"head_sha":"abc"`, 1),
		"bad class":     strings.Replace(good, `"class":"none"`, `"class":"all"`, 1),
		"null runs":     strings.Replace(good, `"runs":[]`, `"runs":null`, 1),
		"bad state":     strings.Replace(good, `"runs":[]`, `"runs":[{"trigger_id":"T-001","level":"L1","state":"ok","reason":null,"fixture_sha256":null,"control":null,"observations":null}]`, 1),
		"no known":      strings.Replace(good, `"runs":[]`, `"runs":[{"trigger_id":"T-001","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{"base":[{"case":"x"}],"head":[]}}]`, 1),
		"obs extra key": strings.Replace(good, `"runs":[]`, `"runs":[{"trigger_id":"T-001","level":"L1","state":"ran","reason":null,"fixture_sha256":null,"control":"valid","observations":{"base":[{"case":"x","known":true,"raw":"secret"}],"head":[]}}]`, 1),
		"bad address":   strings.Replace(good, `"examples":[]`, `"examples":[{"dir":"examples/a","head":"planned","base":null,"addresses":["module.a[\"k\"].x"]}]`, 1),
	} {
		if _, err := ReadLadder(writeJSON(t, "l.json", bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
