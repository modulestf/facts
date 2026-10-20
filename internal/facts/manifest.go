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

// Rule is one implemented rule. A tool rule returns findings; a candidate
// rule returns candidates for the model or the author to confirm.
type Rule struct {
	ID    string
	Tier  string // "tool" or "candidate"
	Class string
	Kinds []string
	Check func(a *Area) Result
}

// Result is what a rule found in one area. A non-empty NotEvaluated means
// the rule could not be decided there.
type Result struct {
	Findings     []Finding
	Candidates   []Candidate
	NotEvaluated string
}

// Candidate is a detected defect a model or an author confirms or rejects.
type Candidate struct {
	CandidateID      string `json:"candidate_id"`
	RuleID           string `json:"rule_id"`
	File             string `json:"file"`
	Line             *int   `json:"line"`
	Subject          string `json:"subject"`
	Evidence         any    `json:"evidence"`
	EvidenceComplete bool   `json:"evidence_complete"`
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
// (rule, area), the findings ordered and numbered as the findings schema
// says, and the candidates numbered by file and line.
func Evaluate(areas []*Area, rules []Rule) ([]ManifestEntry, []Finding, []Candidate) {
	manifest, findings, candidates := []ManifestEntry{}, []Finding{}, []Candidate{}
	for _, r := range rules {
		for _, a := range areas {
			e := ManifestEntry{RuleID: r.ID, Area: a.Path}
			res := Result{NotEvaluated: gate(r, a)}
			if res.NotEvaluated == "" {
				res = r.Check(a)
			}
			switch {
			case res.NotEvaluated == StatusNotApplicable:
				e.Status = StatusNotApplicable
			case res.NotEvaluated != "":
				e.Status, e.Reason = StatusNotEvaluated, res.NotEvaluated
			case len(res.Findings) > 0:
				e.Status = StatusFinding
			case len(res.Candidates) > 0:
				e.Status = StatusCandidate
			default:
				e.Status = StatusClean
			}
			if res.NotEvaluated == "" {
				findings = append(findings, res.Findings...)
				candidates = append(candidates, res.Candidates...)
			}
			manifest = append(manifest, e)
		}
	}
	sortFindings(findings)
	sortCandidates(candidates)
	return manifest, findings, candidates
}

func sortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if la, lb := lineOr0(a.Line), lineOr0(b.Line); la != lb {
			return la < lb
		}
		return a.RuleID < b.RuleID
	})
	for i := range cs {
		cs[i].CandidateID = fmt.Sprintf("C-%03d", i+1)
	}
}

func lineOr0(l *int) int {
	if l == nil {
		return 0
	}
	return *l
}

// gate applies the checks every rule shares before its own logic runs.
// It returns StatusNotApplicable as the reason for a rule that does not apply.
func gate(r Rule, a *Area) string {
	switch {
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

// CheckManifest is the integrity check: exactly one entry per (rule, area)
// for every tool and candidate rule, and no other entry.
func CheckManifest(m []ManifestEntry, rules []Rule, areas []*Area) error {
	want := map[[2]string]bool{}
	for _, r := range rules {
		if r.Tier != "tool" && r.Tier != "candidate" {
			continue
		}
		for _, a := range areas {
			want[[2]string{r.ID, a.Path}] = true
		}
	}
	seen := map[[2]string]bool{}
	for _, e := range m {
		k := [2]string{e.RuleID, e.Area}
		if !want[k] || seen[k] {
			return fmt.Errorf("integrity: unexpected or duplicate manifest entry %s in %s", e.RuleID, e.Area)
		}
		seen[k] = true
	}
	if len(seen) != len(want) {
		return fmt.Errorf("integrity: %d manifest entries, want %d", len(seen), len(want))
	}
	return nil
}
