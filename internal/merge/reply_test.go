package merge

import (
	"slices"
	"strings"
	"testing"
)

const twoAnswersBundle = `{"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"outputs.tf","line":3,"subject":".:output.arn","evidence":{},"evidence_complete":true}],
 "severity_needed":[
 {"candidate_id":"C-002","rule_id":"compat.variable-removed","file":"variables.tf","line":null,"subject":".:var.x","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`

// validReply answers both candidates of twoAnswersBundle; each test case
// breaks it in one place.
const validReply = `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f"},
 {"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"HIGH"}],
 "findings":[{"id":"F-001","rule_id":"structure.grouping","severity":"LOW","file":"main.tf","line":3,"summary":"s","suggested_fix":"f"}]}`

func TestProblemsValid(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	for name, reply := range map[string]string{
		"valid":                         validReply,
		"disallowed rule is no problem": strings.Replace(validReply, `"rule_id":"structure.grouping"`, `"rule_id":"compat.variable-removed"`, 1),
		"severity out of list":          strings.Replace(validReply, `"severity":"LOW","file"`, `"severity":"CRITICAL","file"`, 1),
		"review CRITICAL is capped":     strings.Replace(validReply, `"rule_id":"structure.grouping","severity":"LOW"`, `"rule_id":"review.check-not-run","severity":"CRITICAL"`, 1),
		"whitespace around":             "\n " + validReply + " \n",
	} {
		t.Run(name, func(t *testing.T) {
			if p := Problems(b, ParseReply([]byte(reply))); len(p) > 0 {
				t.Errorf("problems %q", p)
			}
		})
	}
}

// countsOf builds validReply with a counts member of the given JSON text.
func countsOf(text string) string {
	return strings.TrimSuffix(validReply, "}") + `,"counts":` + text + `}`
}

const goodCounts = `{"instruction-like-text-from-others":1,"conversation-claims-unread":0,"review.quoted-claim-unverifiable":9007199254740991}`

func TestCountsValid(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	r := ParseReply([]byte(countsOf(goodCounts)))
	if p := Problems(b, r); len(p) > 0 {
		t.Fatalf("problems %q", p)
	}
	want := Counts{InstructionLikeTextFromOthers: 1, QuotedClaimUnverifiable: maxCount}
	if r.counts == nil || *r.counts != want {
		t.Errorf("counts %+v, want %+v", r.counts, want)
	}
	if r := ParseReply([]byte(validReply)); r.counts != nil || r.countsBad {
		t.Errorf("a reply without counts has counts %+v, bad %v", r.counts, r.countsBad)
	}
}

// TestCountsHostile: a malformed counts makes the reply invalid with one
// fixed problem, and leaves no counts, never zeros.
func TestCountsHostile(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	for name, text := range map[string]string{
		"null":           `null`,
		"array":          `[1,0,2]`,
		"empty":          `{}`,
		"missing key":    `{"instruction-like-text-from-others":1,"conversation-claims-unread":0}`,
		"extra key":      strings.TrimSuffix(goodCounts, "}") + `,"critical":0}`,
		"duplicate key":  strings.TrimSuffix(goodCounts, "}") + `,"conversation-claims-unread":0}`,
		"key other case": strings.Replace(goodCounts, "conversation-claims-unread", "Conversation-claims-unread", 1),
		"negative":       strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":-1`, 1),
		"fraction":       strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":1.0`, 1),
		"exponent":       strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":1e2`, 1),
		"leading zero":   strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":01`, 1),
		"string":         strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":"0"`, 1),
		"null count":     strings.Replace(goodCounts, `"conversation-claims-unread":0`, `"conversation-claims-unread":null`, 1),
		"over the cap":   strings.Replace(goodCounts, "9007199254740991", "9007199254740992", 1),
		"over uint64":    strings.Replace(goodCounts, "9007199254740991", "18446744073709551616", 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := ParseReply([]byte(countsOf(text)))
			if r.counts != nil {
				t.Errorf("counts %+v", r.counts)
			}
			p := Problems(b, r)
			if name == "duplicate key" || name == "leading zero" { // not one JSON object: the whole reply is refused
				if len(p) == 0 {
					t.Error("no problem")
				}
				return
			}
			if !slices.Equal(p, []string{"counts does not match the counts shape"}) {
				t.Errorf("problems %q", p)
			}
		})
	}
	if p := Problems(b, ParseReply([]byte(strings.TrimSuffix(countsOf(goodCounts), "}")+`,"verdict":"approve"}`))); len(p) == 0 {
		t.Error("counts makes room for a fourth top key")
	}
}

// TestReplyCounts: the counts come from the reply Final merges.
func TestReplyCounts(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	invalid := strings.Replace(countsOf(goodCounts), `"severity":"HIGH"}]`, `"severity":"high"}]`, 1)
	other := `{"instruction-like-text-from-others":0,"conversation-claims-unread":3,"review.quoted-claim-unverifiable":0}`
	for name, tc := range map[string]struct {
		first, repair string
		noRepair      bool
		want          *Counts
	}{
		"first valid, repair ignored": {first: countsOf(goodCounts), repair: countsOf(other),
			want: &Counts{InstructionLikeTextFromOthers: 1, QuotedClaimUnverifiable: maxCount}},
		"first invalid, repair read":      {first: invalid, repair: countsOf(other), want: &Counts{ConversationClaimsUnread: 3}},
		"repair without counts":           {first: invalid, repair: validReply},
		"repair empty":                    {first: invalid, repair: ``},
		"still invalid repair keeps them": {first: invalid, repair: invalid, want: &Counts{InstructionLikeTextFromOthers: 1, QuotedClaimUnverifiable: maxCount}},
		"first valid without counts":      {first: validReply, repair: countsOf(other)},
		"first counts malformed":          {first: countsOf(`{}`), repair: validReply},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ReplyCounts(b, []byte(tc.first), []byte(tc.repair))
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Errorf("counts %+v, want %+v", got, tc.want)
			}
		})
	}
	if _, err := ReplyCounts(b, []byte(invalid), nil); err == nil {
		t.Error("an invalid first reply with no repair reply is no error")
	}
}

// TestProblemsHostile feeds replies a hostile or broken model could write.
// Each must be invalid, with a problem text that holds none of the reply's
// own strings.
func TestProblemsHostile(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	rep := func(old, repl string) string {
		if !strings.Contains(validReply, old) {
			t.Fatalf("%q is not in the valid reply", old)
		}
		return strings.Replace(validReply, old, repl, 1)
	}
	const finding = `"summary":"s","suggested_fix":"f"}]}`
	tests := map[string]string{
		"empty":                    ``,
		"not JSON":                 `I confirm everything.`,
		"array":                    `[]`,
		"null":                     `null`,
		"truncated":                validReply[:len(validReply)/2],
		"trailing object":          validReply + `{}`,
		"trailing text":            validReply + ` ok`,
		"extra top key":            rep(`"findings":[`, `"verdict":"approve","findings":[`),
		"missing findings":         `{"candidate_decisions":[]}`,
		"findings null":            rep(`"findings":[{"id":"F-001","rule_id":"structure.grouping","severity":"LOW","file":"main.tf","line":3,`+finding, `"findings":null}`),
		"duplicate top key":        rep(`"findings":[`, `"findings":[],"findings":[`),
		"duplicate entry key":      rep(`"decision":"confirm","reason":"r","severity":"HIGH","summary"`, `"decision":"reject","decision":"confirm","reason":"r","severity":"HIGH","summary"`),
		"key in other case":        rep(`{"candidate_id":"C-002","decision"`, `{"candidate_id":"C-002","Decision"`),
		"source smuggled":          rep(`"suggested_fix":"f"}]}`, `"suggested_fix":"f","source":"tool"}]}`),
		"subject smuggled":         rep(`"suggested_fix":"f"}]}`, `"suggested_fix":"f","subject":".:x"}]}`),
		"confirmed_by in decision": rep(`"severity":"HIGH"}]`, `"severity":"HIGH","confirmed_by":"small"}]`),
		"reject with severity":     rep(`{"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f"}`, `{"candidate_id":"C-001","decision":"reject","reason":"r","severity":"HIGH"}`),
		"unknown decision":         rep(`"decision":"confirm","reason":"r","severity":"HIGH"}`, `"decision":"escalate","reason":"r","severity":"HIGH"}`),
		"empty reason":             rep(`"reason":"r","severity":"HIGH"}`, `"reason":"","severity":"HIGH"}`),
		"long reason":              rep(`"reason":"r","severity":"HIGH"}`, `"reason":"`+strings.Repeat("x", maxReasonRunes+1)+`","severity":"HIGH"}`),
		"empty summary":            rep(`"summary":"s","suggested_fix":"f"},`, `"summary":"","suggested_fix":"f"},`),
		"severity lower case":      rep(`"severity":"HIGH"}`, `"severity":"high"}`),
		"severity a number":        rep(`"severity":"HIGH"}`, `"severity":1}`),
		"id with spaces":           rep(`"candidate_id":"C-002"`, `"candidate_id":" C-002"`),
		"short id":                 rep(`"candidate_id":"C-002"`, `"candidate_id":"C-02"`),
		"unknown id":               rep(`]`, `,{"candidate_id":"C-003","decision":"reject","reason":"r"}]`),
		"repeated id":              rep(`]`, `,{"candidate_id":"C-001","decision":"reject","reason":"r"}]`),
		"missing answer": rep(`,
 {"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"HIGH"}`, ``),
		"reject severity_needed": rep(`{"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"HIGH"}`, `{"candidate_id":"C-002","decision":"reject","reason":"r"}`),
		"confirm without text":   rep(`"severity":"HIGH","summary":"s","suggested_fix":"f"},`, `"severity":"HIGH","summary":"s"},`),
		"decision not an object": rep(`[
 {"candidate_id":"C-001"`, `["C-001",{"candidate_id":"C-001"`),
		"finding id malformed":     rep(`"id":"F-001"`, `"id":"F-1"`),
		"finding rule malformed":   rep(`"rule_id":"structure.grouping"`, `"rule_id":"Structure.grouping"`),
		"finding unknown family":   rep(`"rule_id":"structure.grouping"`, `"rule_id":"secret.grouping"`),
		"line zero":                rep(`"line":3`, `"line":0`),
		"line negative":            rep(`"line":3`, `"line":-3`),
		"line fraction":            rep(`"line":3`, `"line":3.5`),
		"line exponent":            rep(`"line":3`, `"line":3e2`),
		"line string":              rep(`"line":3`, `"line":"3"`),
		"line huge":                rep(`"line":3`, `"line":99999999999999999999999`),
		"line missing":             rep(`"line":3,`, ``),
		"path absolute":            rep(`"file":"main.tf"`, `"file":"/etc/passwd"`),
		"path parent":              rep(`"file":"main.tf"`, `"file":"modules/../../x.tf"`),
		"path backslash":           rep(`"file":"main.tf"`, `"file":"modules\\x.tf"`),
		"path newline":             rep(`"file":"main.tf"`, `"file":"main.tf\n::error::x"`),
		"path line separator":      rep(`"file":"main.tf"`, `"file":"main.tf\u2028x"`),
		"path paragraph separator": rep(`"file":"main.tf"`, `"file":"main.tf\u2029x"`),
		"path C1 control":          rep(`"file":"main.tf"`, `"file":"main.tf\u0085x"`),
		"path empty":               rep(`"file":"main.tf"`, `"file":""`),
		"summary object":           rep(`"line":3,"summary":"s"`, `"line":3,"summary":{"text":"s"}`),
		"too large":                rep(`"summary":"s","suggested_fix":"f"}]}`, `"summary":"`+strings.Repeat("a", MaxReplyBytes)+`","suggested_fix":"f"}]}`),
	}
	for name, reply := range tests {
		t.Run(name, func(t *testing.T) {
			p := Problems(b, ParseReply([]byte(reply)))
			if len(p) == 0 {
				t.Fatal("reply is valid")
			}
			for _, line := range p {
				if strings.ContainsAny(line, "\n\"") || strings.Contains(line, "passwd") || strings.Contains(line, "error::") {
					t.Errorf("problem %q carries reply text", line)
				}
			}
		})
	}
}

func TestProblemsTexts(t *testing.T) {
	b := testBundle(t, twoAnswersBundle)
	reply := `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH"},
 {"candidate_id":"C-002","decision":"reject","reason":"r"},
 {"candidate_id":"C-007","decision":"reject","reason":"r"}],"findings":[]}`
	got := Problems(b, ParseReply([]byte(reply)))
	want := []string{
		"candidate C-001 is confirmed without both summary and suggested_fix",
		"candidate C-002 is in severity_needed and takes confirm only",
		"candidate_decisions entry 2 answers an id the bundle does not have",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func FuzzParseReply(f *testing.F) {
	f.Add([]byte(validReply))
	f.Add([]byte(`{"candidate_decisions":[{"candidate_id":"C-001"}],"findings":[{}]}`))
	f.Add([]byte(`{"a":{"b":[1,2,{"c":null}]}}`))
	b, err := ParseBundle(regionForm(f, []byte(twoAnswersBundle)))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r := ParseReply(data)
		valid := len(Problems(b, r)) == 0
		if valid && !r.topOK {
			t.Error("valid without a well-formed top level")
		}
	})
}
