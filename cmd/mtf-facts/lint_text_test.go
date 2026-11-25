package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// textReport is a report with two findings of one rule, one of another, seven
// candidates over six rules and not evaluated entries of two reasons.
func textReport() *facts.Report {
	r := &facts.Report{
		Findings: []facts.Finding{
			{ID: "F-001", RuleID: "structure.b", Severity: "HIGH", File: "main.tf", Line: new(3), Summary: "b at main", SuggestedFix: "fix b main"},
			{ID: "F-002", RuleID: "examples.a", Severity: "MEDIUM", File: "examples/x", Summary: "a at x", SuggestedFix: "fix a x"},
			{ID: "F-003", RuleID: "examples.a", Severity: "MEDIUM", File: "examples/y", Summary: "a at y\x1b[0m", SuggestedFix: "fix a y"},
		},
		Manifest: []facts.ManifestEntry{
			{RuleID: "examples.a", Area: ".", Status: facts.StatusFinding},
			{RuleID: "coverage.c", Area: ".", Status: facts.StatusNotEvaluated, Reason: "aws_vpc.this: no schema", ReasonCode: facts.ReasonSchemaUnavailable},
			{RuleID: "coverage.c", Area: "modules/m", Status: facts.StatusNotEvaluated, Reason: "aws_vpc.this: no schema", ReasonCode: facts.ReasonSchemaUnavailable},
			{RuleID: "coverage.d", Area: ".", Status: facts.StatusNotEvaluated, Reason: "x: no schema", ReasonCode: facts.ReasonSchemaUnavailable},
			{RuleID: "profile.e", Area: ".", Status: facts.StatusNotEvaluated, Reason: "no profile input", ReasonCode: facts.ReasonProfileUnavailable},
			{RuleID: "compat.f", Area: ".", Status: facts.StatusNotApplicable},
		},
		Scope: &facts.Scope{Change: facts.ChangeNone, AreasChecked: []string{".", "modules/m"}},
	}
	for i, rule := range []string{"s.one", "s.one", "s.two", "s.three", "s.four", "s.five", "s.six"} {
		r.Candidates = append(r.Candidates, facts.Candidate{CandidateID: fmt.Sprintf("C-%03d", i+1), RuleID: rule, File: "main.tf", Line: new(i + 1), Subject: ".:" + rule})
	}
	return r
}

func TestHumanDefaultLayout(t *testing.T) {
	got := human(textReport(), textView{})
	want := `findings: 3 in 2 rules
structure.b (1)
  HIGH main.tf:3: b at main
    fix: fix b main
examples.a (2)
  MEDIUM examples/x: a at x
    fix: fix a x
  MEDIUM examples/y: a at y\x1b[0m
    fix: fix a y
candidates: 7 to confirm or reject (--candidates lists each): s.one 2, s.five 1, s.four 1, s.six 1, s.three 1, and 1 more rule
not evaluated: 4 rule checks (--not-evaluated lists each): schema_unavailable 3 (coverage.c 2, coverage.d 1), profile_unavailable 1 (profile.e 1)
profile rules skipped: no --profile-dir given; pass --profile-dir <dir>, the default profile is .agents/skills/terraform-module-maintainer/profiles/terraform-aws-modules in https://github.com/modulestf/modulestf
schema rules not evaluated: no provider schema given; pass --schema-files <file>, the output of terraform providers schema -json
scope: no change; 2 areas checked
3 findings, 7 candidates; manifest: 1 finding, 0 candidate, 0 clean, 4 not_evaluated, 1 not_applicable
`
	if !strings.HasPrefix(got, want) {
		t.Errorf("got:\n%s\nwant prefix:\n%s", got, want)
	}
}

// The hint lines go when their input is given; a run with nothing to report
// prints no section line.
func TestHumanInputsGiven(t *testing.T) {
	got := human(textReport(), textView{profileGiven: true, schemaGiven: true})
	if strings.Contains(got, "profile rules skipped") || strings.Contains(got, "schema rules not evaluated") {
		t.Errorf("hint with input given:\n%s", got)
	}
	empty := &facts.Report{Manifest: []facts.ManifestEntry{{RuleID: "x.y", Area: ".", Status: facts.StatusClean}}}
	got = human(empty, textView{profileGiven: true})
	for _, s := range []string{"findings:", "candidates:", "not evaluated:", "schema rules"} {
		if strings.Contains(got, s) {
			t.Errorf("%q in a clean report:\n%s", s, got)
		}
	}
}

// The flags print every candidate and not evaluated entry in the per-line
// form, in place of the summary line.
func TestHumanFullLists(t *testing.T) {
	got := human(textReport(), textView{candidates: true, notEvaluated: true, profileGiven: true, schemaGiven: true})
	for _, s := range []string{
		"main.tf:1: unconfirmed s.one: .:s.one, confirm or reject\n",
		"main.tf:7: unconfirmed s.six: .:s.six, confirm or reject\n",
		"not evaluated: coverage.c in modules/m: aws_vpc.this: no schema\n",
		"not evaluated: profile.e in .: no profile input\n",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
	if strings.Contains(got, "lists each") {
		t.Errorf("summary line with the full list:\n%s", got)
	}
}

// The default text of a large report fits one screen: one line per finding
// pair, one per section, whatever the number of candidates and entries.
func TestHumanSizeBounded(t *testing.T) {
	r := &facts.Report{}
	for i := range 500 {
		r.Candidates = append(r.Candidates, facts.Candidate{RuleID: fmt.Sprintf("r.c%d", i%40), File: "main.tf", Subject: "s"})
		r.Manifest = append(r.Manifest, facts.ManifestEntry{RuleID: fmt.Sprintf("r.n%d", i%40), Area: fmt.Sprintf("modules/m%d", i), Status: facts.StatusNotEvaluated, Reason: "x", ReasonCode: facts.ReasonParseError})
	}
	if n := strings.Count(human(r, textView{profileGiven: true}), "\n"); n > 6 {
		t.Errorf("%d lines", n)
	}
}
