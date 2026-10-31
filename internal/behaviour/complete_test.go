package behaviour

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// completeReport is one L1 and L3 trigger with the required cases a and b,
// an inert trigger and an L4-only trigger.
func completeReport() *Report {
	line, addr, attr := 3, "aws_s3_bucket.b", "bucket"
	in := func(v string) map[string]CaseInput { return map[string]CaseInput{"v": {Value: []byte(`"` + v + `"`)}} }
	return &Report{SchemaVersion: 1,
		Entries: []Entry{{Path: "main.tf", ChangeKind: KindModified, TriggerIDs: []string{"T-001", "T-003"}},
			{Path: "README.md", ChangeKind: KindModified, Inert: true, TriggerIDs: []string{"T-002"}}},
		Triggers: []Trigger{
			{ID: "T-001", File: "main.tf", Line: &line, Category: CatWrapper, Address: &addr, Attribute: &attr,
				BaseExpr: new("compact(var.v)"), HeadExpr: new("var.v"),
				Levels: []string{LevelL1, LevelL3}, Cases: []Case{{ID: "a", Inputs: in("a")}, {ID: "b", Inputs: in("b")}}},
			{ID: "T-002", File: "README.md", Category: CatNonHCL, Levels: []string{LevelL4}},
			{ID: "T-003", File: "main.tf", Line: &line, Category: CatLiteral, Address: &addr, Attribute: new("tags"), Levels: []string{LevelL4}},
		}}
}

// obs is a known L1 observation of case c, whose input v is c.
func obs(c, value string) string {
	return `{"case":"` + c + `","known":true,"value":` + value + `,"inputs":{"v":{"value":"` + c + `","unknown":false,"null":false,"omitted":false}}}`
}

const l3Same = `{"case":"prior","known":true,"value":1,"action":"no-op"}`

func stateByID(r *EvalReport, id string) TriggerState {
	for _, s := range r.Triggers {
		if s.ID == id {
			return s
		}
	}
	return TriggerState{}
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestCompleteAndOutstanding(t *testing.T) {
	b := completeReport()
	l3 := sweepRun("T-001", "L3", l3Same, l3Same)
	same := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "1")+","+obs("b", "1"))
	differs := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "2")+","+obs("b", "1"))

	// Complete with no difference: verified, an empty outstanding set.
	s := stateByID(evalWith(t, b, ladder(t, same+","+l3, noCoverage)), "T-001")
	if s.State != StateVerified || s.Complete == nil || !*s.Complete || s.Outstanding == nil || len(s.Outstanding) != 0 || s.EvidenceDigest != "" {
		t.Errorf("no difference: %s", mustJSON(t, s))
	}
	// Complete with an unexplained difference: unverifiable, the difference
	// outstanding, and a digest.
	s = stateByID(evalWith(t, b, ladder(t, differs+","+l3, noCoverage)), "T-001")
	if s.State != StateUnverifiable || s.Complete == nil || !*s.Complete ||
		!slices.Equal(s.Outstanding, []string{"T-001|L1|a|value"}) || !digestPattern.MatchString(s.EvidenceDigest) {
		t.Errorf("outstanding: %s", mustJSON(t, s))
	}
	// A difference verified claims explain is not outstanding.
	explained := evalWith(t, b, ladder(t, differs+","+l3, noCoverage),
		claim("C-1", PredExprEquals, `{"node":"T-001","revision":"base","inputs":{"v":"a"},"expected":1}`),
		claim("C-2", PredExprEquals, `{"node":"T-001","revision":"head","inputs":{"v":"a"},"expected":2}`))
	if s := stateByID(explained, "T-001"); s.Complete == nil || !*s.Complete || len(s.Outstanding) != 0 || s.EvidenceDigest != "" {
		t.Errorf("explained: %s", mustJSON(t, s))
	}

	// complete is absent for an inert and an L4-only trigger.
	r := evalWith(t, b, ladder(t, differs+","+l3, noCoverage))
	for _, id := range []string{"T-002", "T-003"} {
		if s := stateByID(r, id); s.Complete != nil || s.Outstanding != nil || s.EvidenceDigest != "" {
			t.Errorf("%s: %s", id, mustJSON(t, s))
		}
	}
	// The output says complete false, outstanding [] and leaves the digest out.
	if j := string(mustJSON(t, stateByID(evalWith(t, b, ladder(t, same, noCoverage)), "T-001"))); !strings.Contains(j, `"complete": false`) ||
		!strings.Contains(j, `"outstanding": []`) || strings.Contains(j, "evidence_digest") {
		t.Errorf("JSON: %s", j)
	}
}

func TestCompleteFalse(t *testing.T) {
	b := completeReport()
	l3 := sweepRun("T-001", "L3", l3Same, l3Same)
	l1 := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "2")+","+obs("b", "1"))
	sensitive := `{"case":"a","known":true,"sensitive":true,"inputs":{"v":{"value":"a","unknown":false,"null":false,"omitted":false}}}`
	cases := []struct {
		name, runs string
	}{
		{"a required level did not run", l1},
		{"an invalid control", l1 + "," + l3 + `,{"trigger_id":"T-001","level":"L3","state":"unverifiable","reason":"canary","fixture_sha256":null,"control":"invalid","observations":null}`},
		{"a required case is missing", sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "2")) + "," + l3},
		{"an observation is not comparable", sweepRun("T-001", "L1", sensitive+","+obs("b", "1"), sensitive+","+obs("b", "2")) + "," + l3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stateByID(evalWith(t, b, ladder(t, c.runs, noCoverage)), "T-001")
			if s.State != StateUnverifiable || s.Complete == nil || *s.Complete || s.Outstanding == nil {
				t.Errorf("%s", mustJSON(t, s))
			}
		})
	}
}

// TestEvidenceDigest: the digest binds the outstanding ids to the
// observations of those cases, the fixtures and the commits.
func TestEvidenceDigest(t *testing.T) {
	b := completeReport()
	l3 := sweepRun("T-001", "L3", l3Same, l3Same)
	digest := func(runs string, edit func(*Ladder)) (string, []string) {
		t.Helper()
		l := ladder(t, runs, noCoverage)
		if edit != nil {
			edit(l)
		}
		s := stateByID(evalWith(t, b, l), "T-001")
		return s.EvidenceDigest, s.Outstanding
	}
	run := func(aBase, aHead, bBoth string) string {
		return sweepRun("T-001", "L1", obs("a", aBase)+","+obs("b", bBoth), obs("a", aHead)+","+obs("b", bBoth)) + "," + l3
	}
	d0, ids0 := digest(run("1", "2", "1"), nil)
	if d1, _ := digest(run("1", "2", "1"), nil); d1 != d0 || !digestPattern.MatchString(d0) {
		t.Fatalf("not stable: %q %q", d0, d1)
	}
	// Whitespace inside a value is not part of the observation.
	if d, _ := digest(run("[1,2]", "2", "1"), nil); d == d0 {
		t.Error("a list value gives the digest of a number")
	} else if d2, _ := digest(run("[ 1 ,\n 2 ]", "2", "1"), nil); d2 != d {
		t.Error("whitespace inside a value changes the digest")
	}
	// Map keys in another order are the same observation.
	if d, _ := digest(run(`{"x":1,"y":2}`, "2", "1"), nil); d == d0 {
		t.Error("an object value gives the digest of a number")
	} else if d2, _ := digest(run(`{"y":2,"x":1}`, "2", "1"), nil); d2 != d {
		t.Error("key order changes the digest")
	}
	for name, c := range map[string]struct {
		runs string
		edit func(*Ladder)
	}{
		"the head observation":  {run("1", "3", "1"), nil},
		"the base observation":  {run("0", "2", "1"), nil},
		"the head commit":       {run("1", "2", "1"), func(l *Ladder) { l.HeadSHA = new(strings.Repeat("2", 40)) }},
		"the base commit":       {run("1", "2", "1"), func(l *Ladder) { l.BaseSHA = new(strings.Repeat("3", 40)) }},
		"a fixture of the runs": {strings.Replace(run("1", "2", "1"), strings.Repeat("f", 64), strings.Repeat("e", 64), 1), nil},
	} {
		d, ids := digest(c.runs, c.edit)
		if !slices.Equal(ids, ids0) {
			t.Errorf("%s: ids %v, want %v", name, ids, ids0)
		}
		if d == d0 {
			t.Errorf("%s changed and the digest did not", name)
		}
	}
	// A case with no outstanding difference is not part of the evidence.
	if d, _ := digest(run("1", "2", "5"), nil); d != d0 {
		t.Error("a case with no outstanding difference changed the digest")
	}
}

// TestMergedFamilies: a merged trigger that selects the sweep and L4 is
// covered only when both decide it, and complete only when L4 does.
func TestMergedFamilies(t *testing.T) {
	b := completeReport()
	b.Triggers[0].Levels = []string{LevelL1, LevelL3, LevelL4}
	l3 := sweepRun("T-001", "L3", l3Same, l3Same)
	same := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "1")+","+obs("b", "1")) + "," + l3
	differs := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "2")+","+obs("b", "1")) + "," + l3
	cov := `{"class":"complete","examples":[{"dir":".","head":"planned","base":null,"addresses":["aws_s3_bucket.b"],"attributes":{"aws_s3_bucket.b":["bucket"]}}]}`
	for name, c := range map[string]struct {
		runs, cov, state string
		complete         bool
	}{
		"both decide it":            {same, cov, StateCovered, true},
		"the sweep only":            {same, noCoverage, StateUnverifiable, false},
		"the plan only":             {differs, cov, StateUnverifiable, true},
		"neither, differences only": {differs, noCoverage, StateUnverifiable, false},
	} {
		s := stateByID(evalWith(t, b, ladder(t, c.runs, c.cov)), "T-001")
		if s.State != c.state || s.Complete == nil || *s.Complete != c.complete {
			t.Errorf("%s: %s", name, mustJSON(t, s))
		}
	}
}

// TestPR556Merged is the PR 556 L3 replay on the merged trigger: its L1
// and L3 sweeps decide it together.
func TestPR556Merged(t *testing.T) {
	b := pr556Report(t)
	withCases(t, b, "T-001", caseUnknownBase)
	l1 := sweepRun("T-001", "L1", caseUnknownBase, caseUnknownBase)
	l3 := sweepRun("T-001", "L3", `{"case":"prior","known":true,"unknown_mask":false,"action":"no-op","unchanged_vs_prior":true,"value":["sg-1"]}`,
		`{"case":"prior","known":false,"unknown_mask":true,"action":"update","unchanged_vs_prior":false}`)
	claims := []string{
		claim("C-3", PredPlanUnchanged, `{"node":"T-001","revision":"base"}`),
		claim("C-4", PredPlanUnknown, `{"node":"T-001","revision":"head"}`),
		claim("C-5", PredPlanAction, `{"node":"T-001","revision":"base","action":"no-op"}`),
		claim("C-6", PredPlanAction, `{"node":"T-001","revision":"head","action":"update"}`),
	}
	s := stateByID(evalWith(t, b, ladder(t, l1+","+l3, noCoverage), claims...), "T-001")
	if s.State != StateVerified || len(s.Changes) != 3 || s.Complete == nil || !*s.Complete || len(s.Outstanding) != 0 {
		t.Errorf("all claims: %s", mustJSON(t, s))
	}
	s = stateByID(evalWith(t, b, ladder(t, l1+","+l3, noCoverage), claims[:3]...), "T-001")
	if s.State != StateUnverifiable || s.Complete == nil || !*s.Complete ||
		!slices.Equal(s.Outstanding, []string{"T-001|L3|prior|action"}) || !digestPattern.MatchString(s.EvidenceDigest) {
		t.Errorf("without the head action claim: %s", mustJSON(t, s))
	}
	// Without the L3 run the merged trigger is not complete.
	if s := stateByID(evalWith(t, b, ladder(t, l1, noCoverage), claims...), "T-001"); s.State != StateUnverifiable || s.Complete == nil || *s.Complete {
		t.Errorf("L1 only: %s", mustJSON(t, s))
	}
}

// TestCompleteFalseMore: the sweep is incomplete when an L3 case is
// observed at one revision only, when a required L2 level did not run, and
// when the sweep level itself has a run with an invalid control.
func TestCompleteFalseMore(t *testing.T) {
	l3 := sweepRun("T-001", "L3", l3Same, l3Same)
	l1 := sweepRun("T-001", "L1", obs("a", "1")+","+obs("b", "1"), obs("a", "2")+","+obs("b", "1"))
	for name, c := range map[string]struct {
		levels []string
		runs   string
	}{
		"an L3 case at one revision only": {[]string{LevelL1, LevelL3},
			l1 + "," + sweepRun("T-001", "L3", l3Same+`,{"case":"extra","known":true,"value":1}`, l3Same)},
		"a required L2 level did not run": {[]string{LevelL1, LevelL2}, l1},
		"an invalid control at L1": {[]string{LevelL1, LevelL3}, l1 + "," + l3 +
			`,{"trigger_id":"T-001","level":"L1","state":"unverifiable","reason":"canary","fixture_sha256":null,"control":"invalid","observations":null}`},
	} {
		b := completeReport()
		b.Triggers[0].Levels = c.levels
		s := stateByID(evalWith(t, b, ladder(t, c.runs, noCoverage)), "T-001")
		if s.State != StateUnverifiable || s.Complete == nil || *s.Complete {
			t.Errorf("%s: %s", name, mustJSON(t, s))
		}
	}
}

// TestOutstandingCap: more than maxOutstanding unexplained differences
// leave the trigger incomplete with no outstanding ids and no digest.
func TestOutstandingCap(t *testing.T) {
	b := completeReport()
	tr := &b.Triggers[0]
	tr.Levels = []string{LevelL1, LevelL2, LevelL3}
	tr.Cases = nil
	side := func(rev string) string {
		var out []string
		for i := range maxSweepCases {
			id := fmt.Sprintf("c%02d", i)
			if rev == "base" {
				out = append(out, `{"case":"`+id+`","known":true,"value":1,"length":1,"action":"no-op","unchanged_vs_prior":true,"unknown_inputs":["a"],"inputs":{"v":{"value":"`+id+`","unknown":false,"null":false,"omitted":false}}}`)
			} else {
				out = append(out, `{"case":"`+id+`","known":true,"value":2,"length":2,"action":"update","unchanged_vs_prior":false,"unknown_inputs":["b"],"inputs":{"v":{"value":"`+id+`","unknown":false,"null":false,"omitted":false}}}`)
			}
		}
		return strings.Join(out, ",")
	}
	for i := range maxSweepCases {
		id := fmt.Sprintf("c%02d", i)
		tr.Cases = append(tr.Cases, Case{ID: id, Inputs: map[string]CaseInput{"v": {Value: []byte(`"` + id + `"`)}}})
	}
	var runs []string
	for _, lv := range tr.Levels {
		runs = append(runs, sweepRun("T-001", lv, side("base"), side("head")))
	}
	r := evalWith(t, b, ladder(t, strings.Join(runs, ","), noCoverage))
	s := stateByID(r, "T-001")
	if len(s.Changes) <= maxOutstanding {
		t.Fatalf("the fixture has only %d differences", len(s.Changes))
	}
	if s.State != StateUnverifiable || s.Complete == nil || *s.Complete || s.Outstanding != nil || s.EvidenceDigest != "" ||
		s.Reason != fmt.Sprintf("more than %d unexplained differences", maxOutstanding) {
		t.Errorf("over the cap: state %s complete %v outstanding %d digest %q reason %q", s.State, s.Complete, len(s.Outstanding), s.EvidenceDigest, s.Reason)
	}
	if j := string(mustJSON(t, s)); strings.Contains(j, `"outstanding"`) || strings.Contains(j, `"evidence_digest"`) {
		t.Error("over the cap the output names outstanding or a digest")
	}
}
