package facts

import (
	"fmt"
	"path"
	"slices"
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

// Reason codes, from the closed list of not_evaluated reasons.
const (
	ReasonParseError         = "parse_error"
	ReasonUnsupportedFile    = "unsupported_file"
	ReasonSymlink            = "symlink"
	ReasonLimitExceeded      = "limit_exceeded"
	ReasonNoBase             = "no_base"
	ReasonSchemaUnavailable  = "schema_unavailable"
	ReasonProfileUnavailable = "profile_unavailable"
)

// reasonCodes is the closed list, as manifest.schema.json has it.
var reasonCodes = map[string]bool{
	ReasonParseError: true, ReasonUnsupportedFile: true, ReasonSymlink: true, ReasonLimitExceeded: true,
	ReasonNoBase: true, ReasonSchemaUnavailable: true, ReasonProfileUnavailable: true,
}

// Rule classes: a path rule still runs when the configuration cannot be parsed.
const (
	ClassConfig = "config-semantic"
	ClassPath   = "path"
)

// Rule scopes, the registry's scope column: what a rule judges.
const (
	ScopeTouchedBlock      = "touched-block"      // a block the change adds, edits or removes
	ScopeTouchedExpression = "touched-expression" // a value or literal the change sets
	ScopeNewlyIntroduced   = "newly-introduced"   // only what the change adds
	ScopeAnyFileInArea     = "any-file-in-area"   // anything in the area, touched or not
)

// Rule is one implemented rule. A tool rule returns findings; a candidate
// rule returns candidates for the model or the author to confirm.
type Rule struct {
	ID    string
	Tier  string // "tool" or "candidate"
	Class string
	Scope string // one of the Scope constants
	Kinds []string
	// CrossArea is the registry's cross_area: the rule is one pass over every
	// area, with one manifest entry under the root.
	CrossArea bool
	// NeedsBase is set for a rule that cannot be decided without the base
	// tree: with none, every area is not_evaluated(no_base).
	NeedsBase bool
	// ReadsUnreadConfig is set for a rule that also judges a test helper
	// module under tests/ and a directory more than one level under
	// modules/<m>/. No area reads either, so a change to a .tf or .tf.json
	// file there makes the rule not_evaluated at the root or at modules/<m>
	// rather than clean; judgedConfig says which nested files are exempt.
	ReadsUnreadConfig bool
	Check             func(a *Area) Result
}

// rootArea is the area of the module root, where a cross-area rule's one
// manifest entry goes.
const rootArea = "."

// Result is what a rule found in one area. A non-empty NotEvaluated means
// the rule could not be decided there, and Code is that reason's code.
type Result struct {
	Findings     []Finding
	Candidates   []Candidate
	NotEvaluated string
	Code         string
}

// Candidate is a detected defect a model or an author confirms or rejects.
type Candidate struct {
	CandidateID      string   `json:"candidate_id"`
	RuleID           string   `json:"rule_id"`
	File             string   `json:"file"`
	Line             *int     `json:"line"`
	Subject          string   `json:"subject"`
	Evidence         Evidence `json:"evidence"`
	EvidenceComplete bool     `json:"evidence_complete"`
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
	// ReasonCode is set with Reason: one of the Reason constants.
	ReasonCode string `json:"reason_code,omitempty"`
}

// Evaluate runs every rule on every area in scope and returns one manifest
// entry per (rule, area), or one under the root for a cross-area rule, whose
// pass also reads the untouched areas, the findings
// ordered and numbered as the findings schema says, and the candidates
// numbered by file and line. Each rule keeps only the findings and
// candidates its scope judges in ch; see scoped. ch must not be nil.
func Evaluate(areas []*Area, rules []Rule, ch *ChangeMap) ([]ManifestEntry, []Finding, []Candidate) {
	if ch == nil {
		panic("facts: Evaluate needs a change map")
	}
	peers := map[string]*Area{}
	for _, a := range areas {
		peers[a.Path] = a
	}
	for _, a := range areas {
		a.peers = peers
	}
	manifest, findings, candidates := []ManifestEntry{}, []Finding{}, []Candidate{}
	for _, r := range rules {
		if r.CrossArea {
			res := evaluateCrossArea(r, areas, ch)
			manifest = append(manifest, manifestEntry(r.ID, rootArea, res))
			findings = append(findings, res.Findings...)
			candidates = append(candidates, res.Candidates...)
			continue
		}
		for _, a := range areas {
			if a.Untouched {
				continue
			}
			res := scoped(r, evaluate(r, a), ch)
			manifest = append(manifest, manifestEntry(r.ID, a.Path, res))
			if res.NotEvaluated == "" {
				findings = append(findings, res.Findings...)
				candidates = append(candidates, res.Candidates...)
			}
		}
	}
	sortFindings(findings)
	sortCandidates(candidates)
	return manifest, findings, candidates
}

// evaluate runs one rule in one area after the shared gate.
func evaluate(r Rule, a *Area) Result {
	if reason, code := gate(r, a); reason != "" {
		return Result{NotEvaluated: reason, Code: code}
	}
	return r.Check(a)
}

// evaluateCrossArea runs a cross-area rule over every area of the module as
// one pass, untouched areas included. Areas outside its kinds are skipped, so
// the result is never not_applicable. When any area cannot be evaluated, the
// pass is incomplete: the result is not evaluated, with the reason code of the
// first such area, and has no findings or candidates. Otherwise it has the
// findings and candidates of every area that scoped keeps: those on a line in
// the change, or with no line, those whose path is in the change. A result of
// an untouched area is kept only when it is anchored at the change that
// causes it, such as an example that reads an output the change removed:
// its file must itself be in the change, never "." or a directory above a
// changed file.
func evaluateCrossArea(r Rule, areas []*Area, ch *ChangeMap) Result {
	// Without a base tree no area has base facts. A rule that needs them is
	// no_base even when no area is of its kinds.
	if r.NeedsBase && ch.base == nil {
		return Result{NotEvaluated: ReasonNoBase, Code: ReasonNoBase}
	}
	var out Result
	for _, a := range areas {
		res := scoped(r, evaluate(r, a), ch)
		if a.Untouched && res.NotEvaluated == "" {
			res.Findings = slices.DeleteFunc(res.Findings, func(f Finding) bool { return !ch.paths[f.File] })
			res.Candidates = slices.DeleteFunc(res.Candidates, func(c Candidate) bool { return !ch.paths[c.File] })
		}
		switch res.NotEvaluated {
		case StatusNotApplicable:
		case "":
			out.Findings = append(out.Findings, res.Findings...)
			out.Candidates = append(out.Candidates, res.Candidates...)
		default:
			// The reason is the code; a result with no code keeps its text
			// and no code, which CheckManifest refuses.
			if res.Code == "" {
				return Result{NotEvaluated: res.NotEvaluated}
			}
			return Result{NotEvaluated: res.Code, Code: res.Code}
		}
	}
	return out
}

// scoped keeps the findings and candidates of a result that its rule's scope
// judges, and for a cross-area rule only those on lines in the change. When
// that turns on the lines of a file whose diff passed its limit, the result
// is not_evaluated(limit_exceeded).
func scoped(r Rule, res Result, ch *ChangeMap) Result {
	if res.NotEvaluated != "" {
		return res
	}
	unknown := false
	drop := func(file string, line *int) bool {
		in, u := ch.inScope(r.Scope, file, line)
		if in && r.CrossArea {
			in, u = ch.onChangedLine(file, line)
		}
		unknown = unknown || u
		return !in
	}
	res.Findings = slices.DeleteFunc(res.Findings, func(f Finding) bool { return drop(f.File, f.Line) })
	res.Candidates = slices.DeleteFunc(res.Candidates, func(c Candidate) bool { return drop(c.File, c.Line) })
	if unknown {
		return Result{NotEvaluated: "a changed file is past the line diff limit", Code: ReasonLimitExceeded}
	}
	return res
}

// manifestEntry is the entry for one result. Not evaluated takes precedence
// over findings and candidates.
func manifestEntry(rule, area string, res Result) ManifestEntry {
	e := ManifestEntry{RuleID: rule, Area: area}
	switch {
	case res.NotEvaluated == StatusNotApplicable:
		e.Status = StatusNotApplicable
	case res.NotEvaluated != "":
		e.Status, e.Reason, e.ReasonCode = StatusNotEvaluated, res.NotEvaluated, res.Code
	case len(res.Findings) > 0:
		e.Status = StatusFinding
	case len(res.Candidates) > 0:
		e.Status = StatusCandidate
	default:
		e.Status = StatusClean
	}
	return e
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

// gate applies the checks every rule shares before its own logic runs. It
// returns the reason a rule cannot be decided in a, with its code, or
// StatusNotApplicable as the reason for a rule that does not apply.
func gate(r Rule, a *Area) (reason, code string) {
	switch {
	case !slices.Contains(r.Kinds, a.Kind):
		return StatusNotApplicable, ""
	case a.Issue != "":
		return a.Issue, a.issueCode
	case r.NeedsBase && a.Base == nil:
		return "no base revision to compare with", ReasonNoBase
	case r.ReadsUnreadConfig && len(a.unreadConfig) > 0:
		what := ": test helper configuration the engine does not read"
		if a.Kind == AreaSubmodule {
			what = ": configuration in a nested directory or link the engine does not evaluate"
		}
		return strings.Join(a.unreadConfig, ", ") + what, ReasonUnsupportedFile
	}
	if r.Class != ClassConfig {
		return "", ""
	}
	if len(a.Head.Unsupported) > 0 {
		return strings.Join(a.Head.Unsupported, "; "), a.Head.reasonCode()
	}
	if a.Base != nil && len(a.Base.Unsupported) > 0 {
		return "at base: " + strings.Join(a.Base.Unsupported, "; "), a.Base.reasonCode()
	}
	return "", ""
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

func sortStrings(ss ...[]string) {
	for _, s := range ss {
		sort.Strings(s)
	}
}

// CheckManifest is the integrity check: exactly one entry per (rule, area in
// scope) for every tool and candidate rule, one under the root for a cross-area
// rule, no other entry, and a reason code on every not_evaluated entry.
func CheckManifest(m []ManifestEntry, rules []Rule, areas []*Area) error {
	want := map[[2]string]bool{}
	for _, r := range rules {
		if r.Tier != "tool" && r.Tier != "candidate" {
			continue
		}
		if r.CrossArea {
			want[[2]string{r.ID, rootArea}] = true
			continue
		}
		for _, a := range areas {
			if a.Untouched {
				continue
			}
			want[[2]string{r.ID, a.Path}] = true
		}
	}
	seen := map[[2]string]bool{}
	for _, e := range m {
		k := [2]string{e.RuleID, e.Area}
		if !want[k] || seen[k] {
			return fmt.Errorf("integrity: unexpected or duplicate manifest entry %s in %s", e.RuleID, e.Area)
		}
		if e.Status == StatusNotEvaluated && !reasonCodes[e.ReasonCode] {
			return fmt.Errorf("integrity: %s in %s is not evaluated with reason code %q", e.RuleID, e.Area, e.ReasonCode)
		}
		seen[k] = true
	}
	if len(seen) != len(want) {
		return fmt.Errorf("integrity: %d manifest entries, want %d", len(seen), len(want))
	}
	return nil
}
