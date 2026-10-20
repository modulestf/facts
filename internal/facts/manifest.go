package facts

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Manifest statuses.
const (
	StatusClean         = "clean"
	StatusFinding       = "finding"
	StatusCandidate     = "candidate"
	StatusNotEvaluated  = "not_evaluated"
	StatusNotApplicable = "not_applicable"
)

// Rule classes: a path rule still runs when the configuration cannot be parsed.
const (
	ClassConfig = "config-semantic"
	ClassPath   = "path"
)

// Rule is one implemented rule. Check returns the findings for an area, or a
// non-empty reason when the rule cannot be decided there.
type Rule struct {
	ID       string
	Tier     string // "tool" or "candidate"
	Class    string
	Severity string
	Kinds    []string
	Check    func(a *Area) (findings []Finding, notEvaluated string)
}

// Finding has the seven fields of the reviewer's findings schema.
type Finding struct {
	ID           string `json:"id"`
	RuleID       string `json:"rule_id"`
	Severity     string `json:"severity"`
	File         string `json:"file"`
	Line         *int   `json:"line"`
	Summary      string `json:"summary"`
	SuggestedFix string `json:"suggested_fix"`
}

// ManifestEntry is the result of one rule in one area.
type ManifestEntry struct {
	RuleID string `json:"rule_id"`
	Area   string `json:"area"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Evaluate runs every rule on every area and returns one manifest entry per
// (rule, area) and the findings, ordered and numbered as the schema says.
func Evaluate(areas []*Area, rules []Rule) ([]ManifestEntry, []Finding) {
	var manifest []ManifestEntry
	var findings []Finding
	for _, r := range rules {
		for _, a := range areas {
			e := ManifestEntry{RuleID: r.ID, Area: a.Path}
			var fs []Finding
			reason := gate(r, a)
			if reason == "" {
				fs, reason = r.Check(a)
			}
			switch {
			case reason == StatusNotApplicable:
				e.Status = StatusNotApplicable
			case reason != "":
				e.Status, e.Reason = StatusNotEvaluated, reason
			case len(fs) > 0:
				e.Status = StatusFinding
				findings = append(findings, fs...)
			default:
				e.Status = StatusClean
			}
			manifest = append(manifest, e)
		}
	}
	sortFindings(findings)
	return manifest, findings
}

// gate applies the checks every rule shares before its own logic runs.
// It returns StatusNotApplicable as the reason for a rule that does not apply.
func gate(r Rule, a *Area) string {
	switch {
	case a.Kind == AreaUnknown:
		return "the area kind is unknown"
	case !contains(r.Kinds, a.Kind):
		return StatusNotApplicable
	case a.Issue != "":
		return a.Issue
	}
	if r.Class != ClassConfig {
		return ""
	}
	if len(a.Head.Unsupported) > 0 {
		return strings.Join(a.Head.Unsupported, "; ")
	}
	if a.Base != nil && len(a.Base.Unsupported) > 0 {
		return "at base: " + strings.Join(a.Base.Unsupported, "; ")
	}
	return ""
}

var severityRank = map[string]int{"CRITICAL": 0, "HIGH": 1, "MEDIUM": 2, "LOW": 3}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if a.File != b.File {
			return a.File < b.File
		}
		switch {
		case a.Line == nil:
			return false
		case b.Line == nil:
			return true
		}
		return *a.Line < *b.Line
	})
	for i := range fs {
		fs[i].ID = fmt.Sprintf("F-%03d", i+1)
	}
}

// areaFile is a file path relative to the module root.
func areaFile(area, file string) string { return path.Join(area, file) }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func sortStrings(ss ...[]string) {
	for _, s := range ss {
		sort.Strings(s)
	}
}
