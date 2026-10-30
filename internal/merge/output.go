package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// prefixedSubject is a subject with a rule id and "@" in front, which only a
// host review.check-not-run carries.
var prefixedSubject = regexp.MustCompile(`^(compat|coverage|structure|examples|profile|scope|review|claim)\.[a-z0-9]+(-[a-z0-9]+)*@.`)

// checkMerged holds one merged finding to merged-findings.schema.json, so
// the merge never writes a finding the schema refuses.
func checkMerged(f Finding) error {
	var errs []error
	bad := func(cond bool, msg string) {
		if cond {
			errs = append(errs, errors.New(msg))
		}
	}
	bad(!findingIDPattern.MatchString(f.ID), "id is malformed")
	bad(!ruleIDPattern.MatchString(f.RuleID), "rule_id is malformed")
	bad(registry.SeverityRank(f.Severity) < 0, "severity is not a severity")
	bad(strings.HasPrefix(f.RuleID, "review.") && f.Severity == registry.SeverityCritical, "a review.* finding is CRITICAL")
	bad(!validPath(f.File), "file is not a module path")
	bad(f.Line != nil && *f.Line < 1, "line is below 1")
	bad(f.Summary == "" || f.SuggestedFix == "", "summary or suggested_fix is empty")
	bad((f.CandidateID == "") != (f.ConfirmedBy == ""), "candidate_id and confirmed_by are not a pair")
	bad(f.CandidateID != "" && !candidateIDPattern.MatchString(f.CandidateID), "candidate_id is malformed")
	plain := f.Subject == "" || !prefixedSubject.MatchString(f.Subject)
	// A file-level subject, one with no "<area>:" scope, is the finding's
	// file; a review.check-not-run carries it after "<rule_id>@".
	bare := f.Subject
	if !plain {
		_, bare, _ = strings.Cut(f.Subject, "@")
	}
	bad(bare != "" && !strings.Contains(bare, ":") && bare != f.File, "a file-level subject is not the finding's file")
	switch {
	case f.Source == SourceTool:
		bad(f.Subject == "" || !plain, "a tool finding has no plain subject")
		bad(strings.HasPrefix(f.RuleID, "review."), "a tool finding has a review.* rule")
		bad(f.ConfirmedBy != "" && !slices.Contains(confirmClasses[:2], f.ConfirmedBy), "a tool finding is confirmed by lead")
	case f.Source == SourceLLM:
		bad(!plain, "an llm finding has a prefixed subject")
		bad(f.ConfirmedBy != "" && f.ConfirmedBy != "lead", "an llm finding is confirmed by a cheaper class")
		bad(f.CandidateID != "" && f.Subject == "", "a lead-confirmed candidate has no subject")
	case f.Source == SourceHost && f.RuleID == ruleCheckNotRun:
		bad(f.Subject == "" || plain, "a host review.check-not-run has no prefixed subject")
		bad(f.CandidateID != "", "a host review.check-not-run has a candidate_id")
	case f.Source == SourceHost:
		bad(f.Subject == "" || !plain, "a host fallback has no plain subject")
		bad(f.CandidateID == "", "a host fallback has no candidate_id")
		bad(f.ConfirmedBy != "" && !slices.Contains(confirmClasses[:2], f.ConfirmedBy), "a host fallback is confirmed by lead")
	default:
		bad(true, "source is not tool, llm or host")
	}
	return errors.Join(errs...)
}

// MaxTemplateBytes is the largest templates file the merge reads.
const MaxTemplateBytes = 1 << 20

// ParseTemplates reads the host templates: an object whose "rules" member
// maps a rule id to an object with "summary_template" and
// "suggested_fix_template". Other members are ignored, so a file that also
// carries registry columns, as the merge fixtures' registry.json does, is
// read as is. A rule with only one of the two templates is refused.
func ParseTemplates(data []byte) (map[string]Template, error) {
	if len(data) > MaxTemplateBytes {
		return nil, fmt.Errorf("templates are larger than %d bytes", MaxTemplateBytes)
	}
	var doc struct {
		Rules map[string]struct {
			Summary      string `json:"summary_template"`
			SuggestedFix string `json:"suggested_fix_template"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("templates: %w", err)
	}
	out := map[string]Template{}
	for id, t := range doc.Rules {
		switch {
		case t.Summary == "" && t.SuggestedFix == "":
			continue
		case !ruleIDPattern.MatchString(id):
			return nil, fmt.Errorf("templates: %q is not a rule id", id)
		case t.Summary == "" || t.SuggestedFix == "":
			return nil, fmt.Errorf("templates: rule %s has only one of its two templates", id)
		}
		out[id] = Template{Summary: t.Summary, SuggestedFix: t.SuggestedFix}
	}
	return out, nil
}

// MaxReportBytes is the largest lint report the merge reads for its index.
const MaxReportBytes = 64 << 20

// ParseIndex reads the index of a lint report, as mtf-facts lint --json
// writes it. A report with no index has none; a AI report, which leaves
// its index out, is an error.
func ParseIndex(data []byte) ([]facts.IndexEntry, error) {
	if len(data) > MaxReportBytes {
		return nil, fmt.Errorf("report is larger than %d bytes", MaxReportBytes)
	}
	var r struct {
		Index []facts.IndexEntry `json:"index"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	if facts.IsAIReport(data) {
		return nil, fmt.Errorf("report: %w", facts.ErrAIReport)
	}
	for i, e := range r.Index {
		if e.Subject == "" || !validPath(e.File) || e.StartLine < 1 || e.EndLine < e.StartLine {
			return nil, fmt.Errorf("report: index entry %d is malformed", i)
		}
	}
	return r.Index, nil
}

// Write writes merged findings as indented JSON, an empty list as [].
func Write(findings []Finding) ([]byte, error) {
	if findings == nil {
		findings = []Finding{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(findings); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
