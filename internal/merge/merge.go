package merge

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// Rules the merge itself writes or allows.
const (
	ruleCheckNotRun  = "review.check-not-run"
	ruleToolDisputed = "review.tool-disputed"
)

// classPath is the registry class of a path rule.
const classPath = "path"

// toolDisputed stands in for review.tool-disputed while the pinned registry
// has no row for it: an llm path rule at LOW, as the row on the skills
// default branch has it.
var toolDisputed = registry.Rule{
	ID: ruleToolDisputed, Engine: registry.EngineLLM, Class: classPath,
	Severities: []string{registry.SeverityLow},
}

// Template is the host text of one rule, with {subject} and {rule_id} as
// placeholders.
type Template struct {
	Summary      string
	SuggestedFix string
}

// checkNotRunTemplate is the text of a review.check-not-run for a candidate
// when the templates have none for that rule.
var checkNotRunTemplate = Template{
	Summary:      "Rule {rule_id} was not decided for {subject}: the model reply was still invalid after the repair turn.",
	SuggestedFix: "Review {subject} against {rule_id} by hand.",
}

func (t Template) fill(ruleID, subject string) (string, string) {
	r := strings.NewReplacer("{subject}", subject, "{rule_id}", ruleID)
	return r.Replace(t.Summary), r.Replace(t.SuggestedFix)
}

// Inputs are what the final merge reads besides the replies.
type Inputs struct {
	Registry *registry.Registry
	Bundle   *Bundle
	// Index is the facts index of the lint report, which derives the subject
	// of a lead finding. Without one, lead findings dedup by location.
	Index []facts.IndexEntry
	// Templates are the per-rule host templates. The merge needs one for a
	// severity_needed entry it materializes without model text.
	Templates map[string]Template
}

// Final merges the bundle with the lead's reply into the merged findings.
// repair is the reply to the repair turn, nil when none ran. The first reply
// is merged when it is valid, and repair is then ignored. Otherwise repair
// replaces it whole: all of it when it is valid, only its valid entries when
// it is not. An empty or unreadable repair reply keeps nothing.
func Final(in Inputs, first, repair []byte) ([]Finding, error) {
	if err := in.check(); err != nil {
		return nil, err
	}
	reply := ParseReply(first)
	if len(Problems(in.Bundle, reply)) > 0 {
		if repair == nil {
			return nil, errors.New("the first reply is invalid and no repair reply was given")
		}
		reply = ParseReply(repair)
	}
	items, err := in.collect(reply)
	if err != nil {
		return nil, err
	}
	items = in.dedupStep2(dedupStep1(items))
	if err := in.checkSeverityNeeded(items); err != nil {
		return nil, err
	}
	slices.SortStableFunc(items, func(a, b *item) int { return finalCmp(&a.f, &b.f) })
	out := make([]Finding, len(items))
	for i, it := range items {
		out[i] = it.f
		out[i].ID = fmt.Sprintf("F-%03d", i+1)
		if err := checkMerged(out[i]); err != nil {
			return nil, fmt.Errorf("merged finding %s: %w", out[i].ID, err)
		}
	}
	return out, nil
}

// check holds the bundle to the registry: every rule it names has a row, and
// a severity_needed entry lists its rule's severities.
func (in Inputs) check() error {
	var errs []error
	known := func(where, id string) (registry.Rule, bool) {
		r, ok := in.rule(id)
		if !ok {
			errs = append(errs, fmt.Errorf("%s: rule %q is not in the registry", where, id))
		}
		return r, ok
	}
	for _, f := range in.Bundle.ToolFindings {
		if r, ok := known("tool finding", f.RuleID); ok && len(r.Severities) == 0 {
			errs = append(errs, fmt.Errorf("tool finding: rule %s has no severities", f.RuleID))
		}
	}
	for _, c := range in.Bundle.Candidates {
		if r, ok := known("candidate "+c.CandidateID, c.RuleID); ok && len(r.Severities) == 0 {
			errs = append(errs, fmt.Errorf("candidate %s: rule %s has no severities", c.CandidateID, c.RuleID))
		}
	}
	for _, s := range in.Bundle.SeverityNeeded {
		r, ok := known("severity_needed "+s.CandidateID, s.RuleID)
		switch {
		case ok && len(r.Severities) == 0:
			errs = append(errs, fmt.Errorf("severity_needed %s: rule %s has no severities", s.CandidateID, s.RuleID))
		case ok && !sameSet(r.Severities, s.Severities):
			errs = append(errs, fmt.Errorf("severity_needed %s: severities %v, registry %v", s.CandidateID, s.Severities, r.Severities))
		}
	}
	if r, ok := in.rule(ruleCheckNotRun); !ok || len(r.Severities) == 0 {
		errs = append(errs, fmt.Errorf("rule %q is not in the registry with severities", ruleCheckNotRun))
	}
	for _, o := range in.Registry.Overlaps {
		if _, ok := knownOverlaps[[2]string{o.KeptCell, o.DroppedCell}]; !ok {
			errs = append(errs, fmt.Errorf("dedup overlap pair %d is not one the merge implements", o.Pair))
		}
	}
	return errors.Join(errs...)
}

// checkSeverityNeeded checks that each severity_needed id is materialized
// exactly once after dedup.
func (in Inputs) checkSeverityNeeded(items []*item) error {
	count := map[string]int{}
	for _, it := range items {
		count[it.f.CandidateID]++
	}
	var errs []error
	for _, s := range in.Bundle.SeverityNeeded {
		if n := count[s.CandidateID]; n != 1 {
			errs = append(errs, fmt.Errorf("severity_needed %s is materialized %d times", s.CandidateID, n))
		}
	}
	return errors.Join(errs...)
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// rule returns a registry row, with the stand-in for review.tool-disputed.
func (in Inputs) rule(id string) (registry.Rule, bool) {
	if r, ok := in.Registry.Rule(id); ok {
		return r, true
	}
	if id == ruleToolDisputed {
		return toolDisputed, true
	}
	return registry.Rule{}, false
}

// item is a finding on its way through the merge. subject is the dedup
// subject: the finding's own, or the one derived for a lead finding, which
// the output does not carry. arrival orders a full tie: tool findings, then
// candidate answers, then the lead's findings. final marks a bundle tool
// finding.
type item struct {
	f       Finding
	subject string
	arrival int
	final   bool
}

// key is the step 1 dedup key: the rule and the subject, or the rule and the
// location when there is no subject.
func (it *item) key() string {
	if it.subject != "" {
		return "s\x00" + it.f.RuleID + "\x00" + it.subject
	}
	return "l\x00" + it.f.RuleID + "\x00" + it.locus()
}

func (it *item) locus() string {
	line := "null"
	if it.f.Line != nil {
		line = strconv.Itoa(*it.f.Line)
	}
	return it.f.File + "\x00" + line
}

// collect turns the bundle and the reply into items, in arrival order.
func (in Inputs) collect(r *Reply) ([]*item, error) {
	var items []*item
	add := func(f Finding, subject string, final bool) {
		items = append(items, &item{f: f, subject: subject, arrival: len(items), final: final})
	}
	for _, f := range in.Bundle.ToolFindings {
		add(f, f.Subject, true)
	}
	answers, err := in.answers(r)
	if err != nil {
		return nil, err
	}
	for _, f := range answers {
		add(f, f.Subject, false)
	}
	for _, e := range r.findings {
		if !e.ok {
			continue
		}
		rule, ok := in.allowed(e.f)
		if !ok {
			continue
		}
		f := e.f
		f.Severity = clamp(f.Severity, rule.Severities)
		add(f, in.derivedSubject(rule, f), false)
	}
	return items, nil
}

// answers materializes every candidate and severity_needed entry, in bundle
// order: from its valid answer, or from the host when the answer is invalid
// or missing.
func (in Inputs) answers(r *Reply) ([]Finding, error) {
	var out []Finding
	for _, c := range in.Bundle.Candidates {
		d, st := r.answer(c.CandidateID, false)
		switch {
		case st != answerValid:
			out = append(out, in.checkNotRun(c))
		case d.Confirm:
			rule, _ := in.rule(c.RuleID)
			out = append(out, Finding{
				RuleID: c.RuleID, Severity: clamp(d.Severity, rule.Severities), File: c.File, Line: c.Line,
				Summary: d.Summary, SuggestedFix: d.SuggestedFix, Source: SourceLLM, Subject: c.Subject,
				CandidateID: c.CandidateID, ConfirmedBy: "lead",
			})
		}
	}
	for _, s := range in.Bundle.SeverityNeeded {
		f := Finding{
			RuleID: s.RuleID, File: s.File, Line: s.Line, Subject: s.Subject,
			CandidateID: s.CandidateID, ConfirmedBy: s.ConfirmedBy,
		}
		f.Summary, f.SuggestedFix = in.Templates[s.RuleID].fill(s.RuleID, s.Subject)
		if d, st := r.answer(s.CandidateID, true); st == answerValid {
			f.Source, f.Severity = SourceTool, clamp(d.Severity, s.Severities)
			f.Summary, f.SuggestedFix = cmp.Or(d.Summary, f.Summary), cmp.Or(d.SuggestedFix, f.SuggestedFix)
		} else {
			f.Source, f.Severity = SourceHost, highest(s.Severities)
		}
		if f.Summary == "" || f.SuggestedFix == "" {
			return nil, fmt.Errorf("severity_needed %s: no template for rule %s", s.CandidateID, s.RuleID)
		}
		out = append(out, f)
	}
	return out, nil
}

// checkNotRun is the host finding for a candidate the reply left undecided.
// Its subject carries the candidate's rule, so two rules on one address stay
// two findings.
func (in Inputs) checkNotRun(c Candidate) Finding {
	cnr, _ := in.rule(ruleCheckNotRun)
	t, ok := in.Templates[ruleCheckNotRun]
	if !ok {
		t = checkNotRunTemplate
	}
	summary, fix := t.fill(c.RuleID, c.Subject)
	return Finding{
		RuleID: ruleCheckNotRun, Severity: highest(cnr.Severities), File: c.File, Line: c.Line,
		Summary: summary, SuggestedFix: fix, Source: SourceHost, Subject: c.RuleID + "@" + c.Subject,
	}
}

// allowed returns the rule of a lead finding when the lead may use it: an
// llm rule, or a rule injected as not_evaluated for the finding's area, or
// for any area when the rule is cross-area, whose one entry is under ".".
func (in Inputs) allowed(f Finding) (registry.Rule, bool) {
	r, ok := in.rule(f.RuleID)
	if !ok || len(r.Severities) == 0 {
		return r, false
	}
	if r.Engine == registry.EngineLLM {
		return r, true
	}
	area, ok := findingArea(r, f.File)
	if !ok {
		return r, false
	}
	for _, ar := range in.Bundle.AreaRules {
		if ar.Area != area && (!r.CrossArea || ar.Area != ".") {
			continue
		}
		for _, rr := range ar.Rules {
			if rr.RuleID == f.RuleID && rr.Status == "not_evaluated" {
				return r, true
			}
		}
	}
	return r, false
}

// findingArea is the area of a finding's file, as the facts engine maps a
// path. A path rule's subject may be an area directory itself, such as
// examples/complete, which facts.Classify reads as a loose file under
// examples/; for a path rule that directory is its own area.
func findingArea(r registry.Rule, file string) (string, bool) {
	if area, _, ok := facts.Classify(file); ok {
		return area, true
	}
	if parts := strings.Split(file, "/"); r.Class == classPath && len(parts) == 2 &&
		(parts[0] == "examples" || parts[0] == "modules") && parts[1] != "" {
		return file, true
	}
	return "", false
}

// derivedSubject is the dedup subject of a lead finding: the file for a
// path rule, before the index is read, else the subject the index gives its
// line. It is "" when neither applies, and always for a review.* finding,
// which dedups by rule, file and line: two review.check-not-run findings
// for different rules in one file are two findings, though the registry
// makes the rule a path rule.
func (in Inputs) derivedSubject(r registry.Rule, f Finding) string {
	if strings.HasPrefix(f.RuleID, "review.") {
		return ""
	}
	if r.Class == classPath {
		return f.File
	}
	if f.Line == nil {
		return ""
	}
	return facts.SubjectAt(in.Index, f.File, *f.Line)
}

// finalCmp is the Final Step sort: severity, CRITICAL first, then file, then
// line with null last.
func finalCmp(a, b *Finding) int {
	if c := cmp.Compare(registry.SeverityRank(a.Severity), registry.SeverityRank(b.Severity)); c != 0 {
		return c
	}
	if c := cmp.Compare(a.File, b.File); c != 0 {
		return c
	}
	switch {
	case a.Line == nil && b.Line == nil:
		return 0
	case a.Line == nil:
		return 1
	case b.Line == nil:
		return -1
	}
	return cmp.Compare(*a.Line, *b.Line)
}

// clamp caps a severity above the permitted list down to the highest
// permitted one at or below it, and raises one below the list to the lowest.
func clamp(sev string, permitted []string) string {
	if len(permitted) == 0 { // Inputs.check refuses such a rule; never index an empty list
		return sev
	}
	ranked := slices.SortedFunc(slices.Values(permitted), func(a, b string) int {
		return cmp.Compare(registry.SeverityRank(a), registry.SeverityRank(b))
	})
	for _, p := range ranked {
		if registry.SeverityRank(p) >= registry.SeverityRank(sev) {
			return p
		}
	}
	return ranked[len(ranked)-1]
}

// highest returns the most severe permitted severity.
func highest(permitted []string) string { return clamp(registry.SeverityCritical, permitted) }
