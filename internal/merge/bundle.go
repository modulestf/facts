package merge

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	fb "github.com/modulestf/facts/internal/bundle"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// MaxBundleBytes is the largest bundle the merge reads. The bundle cap is 48
// KB of payload; this only bounds a broken host.
const MaxBundleBytes = 4 << 20

// Sources of a merged finding: the channel it arrived on.
const (
	SourceTool = "tool"
	SourceLLM  = "llm"
	SourceHost = "host"
)

// Confirm classes, cheapest first.
var confirmClasses = []string{"small", "mid", "lead"}

// Finding is one merged finding: the seven finding fields and the host-owned
// provenance of merged-findings.schema.json. A bundle tool finding has the
// same shape.
type Finding struct {
	ID           string `json:"id"`
	RuleID       string `json:"rule_id"`
	Severity     string `json:"severity"`
	File         string `json:"file"`
	Line         *int   `json:"line"`
	Summary      string `json:"summary"`
	SuggestedFix string `json:"suggested_fix"`
	Source       string `json:"source"`
	Subject      string `json:"subject,omitempty"`
	CandidateID  string `json:"candidate_id,omitempty"`
	ConfirmedBy  string `json:"confirmed_by,omitempty"`
}

// Candidate, SeverityNeeded and AreaRules are the bundle's own types.
type (
	Candidate      = facts.Candidate
	SeverityNeeded = fb.SeverityNeeded
	AreaRules      = fb.AreaRules
)

// Bundle is the part of the facts bundle the merge reads. A shed area_rules
// section is nil.
type Bundle struct {
	ToolFindings   []Finding
	Candidates     []Candidate
	AreaRules      []AreaRules
	SeverityNeeded []SeverityNeeded
}

// ParseBundle reads a bundle file in the region form mtf-facts bundle
// writes, with bundle.Decode, and checks what the merge relies on:
// well-formed candidate ids that appear once across candidates,
// severity_needed and tool findings, and well-formed paths.
func ParseBundle(data []byte) (*Bundle, error) {
	if len(data) > MaxBundleBytes {
		return nil, fmt.Errorf("bundle is larger than %d bytes", MaxBundleBytes)
	}
	d, err := fb.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := &Bundle{Candidates: d.Candidates, SeverityNeeded: d.SeverityNeeded}
	if d.AreaRules != nil {
		b.AreaRules = *d.AreaRules
	}
	for _, t := range d.ToolFindings {
		b.ToolFindings = append(b.ToolFindings, Finding{
			ID: t.ID, RuleID: t.RuleID, Severity: t.Severity, File: t.File, Line: t.Line,
			Summary: t.Summary, SuggestedFix: t.SuggestedFix,
			Source: t.Source, Subject: t.Subject, CandidateID: t.CandidateID, ConfirmedBy: t.ConfirmedBy,
		})
	}
	if err := b.check(); err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	return b, nil
}

func (b *Bundle) check() error {
	var errs []error
	seen := map[string]bool{}
	id := func(where, id string) {
		switch {
		case !candidateIDPattern.MatchString(id):
			errs = append(errs, fmt.Errorf("%s: candidate id %q is malformed", where, id))
		case seen[id]:
			errs = append(errs, fmt.Errorf("%s: candidate id %s appears twice", where, id))
		}
		seen[id] = true
	}
	for i, f := range b.ToolFindings {
		where := fmt.Sprintf("tool finding %d", i)
		if f.CandidateID != "" {
			id(where, f.CandidateID)
		}
		if f.Source != SourceTool || f.Subject == "" || (f.CandidateID == "") != (f.ConfirmedBy == "") ||
			(f.ConfirmedBy != "" && !slices.Contains(confirmClasses[:2], f.ConfirmedBy)) {
			errs = append(errs, fmt.Errorf("%s: source, subject, candidate_id or confirmed_by is malformed", where))
		}
		if !validPath(f.File) || !ruleIDPattern.MatchString(f.RuleID) || registry.SeverityRank(f.Severity) < 0 ||
			f.Summary == "" || f.SuggestedFix == "" {
			errs = append(errs, fmt.Errorf("%s: a finding field is malformed", where))
		}
	}
	for i, c := range b.Candidates {
		where := fmt.Sprintf("candidate %d", i)
		id(where, c.CandidateID)
		if !validPath(c.File) || !ruleIDPattern.MatchString(c.RuleID) || c.Subject == "" {
			errs = append(errs, fmt.Errorf("%s: rule_id, file or subject is malformed", where))
		}
	}
	for i, s := range b.SeverityNeeded {
		where := fmt.Sprintf("severity_needed %d", i)
		id(where, s.CandidateID)
		if !validPath(s.File) || !ruleIDPattern.MatchString(s.RuleID) || s.Subject == "" ||
			!slices.Contains(confirmClasses[:2], s.ConfirmedBy) {
			errs = append(errs, fmt.Errorf("%s: rule_id, file, subject or confirmed_by is malformed", where))
		}
	}
	return errors.Join(errs...)
}

// answerID is a candidate id the lead must answer.
type answerID struct {
	id     string
	needed bool // from severity_needed
}

// answerIDs returns the ids of candidates and severity_needed, in that
// order.
func (b *Bundle) answerIDs() []answerID {
	out := make([]answerID, 0, len(b.Candidates)+len(b.SeverityNeeded))
	for _, c := range b.Candidates {
		out = append(out, answerID{id: c.CandidateID})
	}
	for _, s := range b.SeverityNeeded {
		out = append(out, answerID{id: s.CandidateID, needed: true})
	}
	return out
}
