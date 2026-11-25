package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/facts"
)

// maxSummaryRules caps the rules named on a candidates or not evaluated
// summary line; the rest are counted.
const maxSummaryRules = 5

// defaultProfileHint names where the default profile lives, for a run with no
// --profile-dir.
const defaultProfileHint = "profile rules skipped: no --profile-dir given; pass --profile-dir <dir>, " +
	"the default profile is .agents/skills/terraform-module-maintainer/profiles/terraform-aws-modules in https://github.com/modulestf/modulestf"

const schemaHint = "schema rules not evaluated: no provider schema given; pass --schema-files <file>, " +
	"the output of terraform providers schema -json"

// textView is what the text report shows beyond its default: the full
// candidate and not evaluated lists, and whether the profile and a schema
// were given, which decides the hint lines.
type textView struct {
	candidates, notEvaluated  bool
	profileGiven, schemaGiven bool
}

// human is the text report: the findings grouped by rule, one summary line
// each for the candidates and the rules not evaluated unless v asks for the
// full lists, a hint for a missing profile or schema, then the callers, the
// scope, the counts and the version.
func human(r *facts.Report, v textView) string {
	var b strings.Builder
	writeFindings(&b, r.Findings)
	if v.candidates {
		for _, c := range r.Candidates {
			fmt.Fprintf(&b, "%s: unconfirmed %s: %s, confirm or reject\n", clean(location(c.File, c.Line)), c.RuleID, clean(c.Subject))
		}
	} else if len(r.Candidates) > 0 {
		by := map[string]int{}
		for _, c := range r.Candidates {
			by[c.RuleID]++
		}
		fmt.Fprintf(&b, "candidates: %d to confirm or reject (--candidates lists each): %s\n", len(r.Candidates), ruleCounts(by))
	}
	counts := map[string]int{}
	byReason := map[string]map[string]int{}
	for _, e := range r.Manifest {
		counts[e.Status]++
		if e.Status != facts.StatusNotEvaluated {
			continue
		}
		if v.notEvaluated {
			fmt.Fprintf(&b, "not evaluated: %s in %s: %s\n", e.RuleID, clean(e.Area), clean(e.Reason))
			continue
		}
		if byReason[e.ReasonCode] == nil {
			byReason[e.ReasonCode] = map[string]int{}
		}
		byReason[e.ReasonCode][e.RuleID]++
	}
	if n := counts[facts.StatusNotEvaluated]; n > 0 && !v.notEvaluated {
		var parts []string
		for _, code := range sortedByCount(byReason, func(m map[string]int) int { return sum(m) }) {
			parts = append(parts, fmt.Sprintf("%s %d (%s)", clean(code), sum(byReason[code]), ruleCounts(byReason[code])))
		}
		fmt.Fprintf(&b, "not evaluated: %d rule checks (--not-evaluated lists each): %s\n", n, strings.Join(parts, ", "))
	}
	if !v.profileGiven {
		fmt.Fprintln(&b, defaultProfileHint)
	}
	if !v.schemaGiven && hasReason(r.Manifest, facts.ReasonSchemaUnavailable) {
		fmt.Fprintln(&b, schemaHint)
	}
	if r.Callers != nil {
		writeCallers(&b, *r.Callers)
	}
	if r.Scope != nil {
		writeScope(&b, r.Scope)
	}
	fmt.Fprintf(&b, "%d findings, %d candidates; manifest: %d finding, %d candidate, %d clean, %d not_evaluated, %d not_applicable\n",
		len(r.Findings), len(r.Candidates), counts[facts.StatusFinding], counts[facts.StatusCandidate],
		counts[facts.StatusClean], counts[facts.StatusNotEvaluated], counts[facts.StatusNotApplicable])
	reg := "none"
	if r.Envelope.RegistrySHA256 != nil {
		reg = *r.Envelope.RegistrySHA256
	}
	fmt.Fprintf(&b, "mtf-facts %s, binary sha256 %s, registry sha256 %s\n", r.Envelope.Tool.Version, r.Envelope.Tool.BinarySHA256, reg)
	return b.String()
}

// writeFindings writes every finding, grouped by rule in the order the report
// ranks them, each with its own summary and fix: there is no per-rule text,
// and a finding's summary names its own subject.
func writeFindings(b *strings.Builder, fs []facts.Finding) {
	if len(fs) == 0 {
		return
	}
	var order []string
	group := map[string][]facts.Finding{}
	for _, f := range fs {
		if group[f.RuleID] == nil {
			order = append(order, f.RuleID)
		}
		group[f.RuleID] = append(group[f.RuleID], f)
	}
	fmt.Fprintf(b, "findings: %d in %d %s\n", len(fs), len(order), plural(len(order), "rule", "rules"))
	for _, rule := range order {
		fmt.Fprintf(b, "%s (%d)\n", rule, len(group[rule]))
		for _, f := range group[rule] {
			fmt.Fprintf(b, "  %s %s: %s\n    fix: %s\n", f.Severity, clean(location(f.File, f.Line)), clean(f.Summary), clean(f.SuggestedFix))
		}
	}
}

// ruleCounts lists the rules of by with their counts, the most first, at most
// maxSummaryRules of them, and counts the rest.
func ruleCounts(by map[string]int) string {
	rules := sortedByCount(by, func(n int) int { return n })
	var parts []string
	for _, r := range rules[:min(len(rules), maxSummaryRules)] {
		parts = append(parts, fmt.Sprintf("%s %d", r, by[r]))
	}
	if rest := len(rules) - maxSummaryRules; rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more %s", rest, plural(rest, "rule", "rules")))
	}
	return strings.Join(parts, ", ")
}

// sortedByCount returns the keys of m by weight, the largest first, ties by key.
func sortedByCount[V any](m map[string]V, weight func(V) int) []string {
	return slices.SortedFunc(maps.Keys(m), func(a, b string) int {
		return cmp.Or(cmp.Compare(weight(m[b]), weight(m[a])), cmp.Compare(a, b))
	})
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func hasReason(m []facts.ManifestEntry, code string) bool {
	return slices.ContainsFunc(m, func(e facts.ManifestEntry) bool {
		return e.Status == facts.StatusNotEvaluated && e.ReasonCode == code
	})
}

func location(file string, line *int) string {
	if line == nil {
		return file
	}
	return fmt.Sprintf("%s:%d", file, *line)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
