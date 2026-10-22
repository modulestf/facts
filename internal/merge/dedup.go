package merge

import (
	"cmp"
	"regexp"
	"slices"

	"github.com/modulestf/facts/internal/registry"
)

// dedupStep1 applies "tool findings are final", then merges duplicates by
// key. A finding other than a bundle tool finding is dropped when a tool
// finding has its key, or, when it has no subject, its rule and location.
func dedupStep1(items []*item) []*item {
	toolKeys, toolLoci := map[string]bool{}, map[string]bool{}
	var tool, rest []*item
	for _, it := range items {
		if it.final {
			tool = append(tool, it)
			toolKeys[it.key()] = true
			toolLoci[it.f.RuleID+"\x00"+it.locus()] = true
		} else {
			rest = append(rest, it)
		}
	}
	rest = slices.DeleteFunc(rest, func(it *item) bool {
		if it.subject == "" {
			return toolLoci[it.f.RuleID+"\x00"+it.locus()]
		}
		return toolKeys[it.key()]
	})
	return append(mergeGroups(tool), mergeGroups(rest)...)
}

// mergeGroups merges the items that share a key into one each, in the order
// of each key's first item.
func mergeGroups(items []*item) []*item {
	var order []string
	groups := map[string][]*item{}
	for _, it := range items {
		k := it.key()
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], it)
	}
	out := make([]*item, 0, len(order))
	for _, k := range order {
		out = append(out, mergeGroup(groups[k]))
	}
	return out
}

// mergeGroup merges duplicates: the highest severity, the text of the first
// in the Final Step sort with arrival order on a full tie, and the
// provenance of the cheapest confirmer, so confirmed_by stays one value.
func mergeGroup(g []*item) *item {
	if len(g) == 1 {
		return g[0]
	}
	slices.SortStableFunc(g, func(a, b *item) int {
		return cmp.Or(finalCmp(&a.f, &b.f), cmp.Compare(a.arrival, b.arrival))
	})
	text, base := g[0], g[0]
	for _, it := range g {
		if it.f.ConfirmedBy != "" && (base.f.ConfirmedBy == "" || classRank(it.f.ConfirmedBy) < classRank(base.f.ConfirmedBy)) {
			base = it
		}
	}
	merged := *base
	// The first in the sort has the highest severity.
	merged.f.Severity = text.f.Severity
	merged.f.Summary, merged.f.SuggestedFix = text.f.Summary, text.f.SuggestedFix
	return &merged
}

func classRank(c string) int { return slices.Index(confirmClasses, c) }

// overlapRule is how the merge reads one row of the registry's Dedup
// Overlap table. Kept and Dropped come from the row's patterns; a family
// pattern is wider than the row's prose, so notKept lists the rules the row
// excludes from it, and sameDefect is the row's "Same defect when".
type overlapRule struct {
	notKept    []string
	sameDefect func(kept, dropped *item) bool
}

// knownOverlaps are the rows the merge implements, by their Kept and Dropped
// cells as written. The merge refuses a registry with any other row, so a
// new or reworded row needs code here first.
var knownOverlaps = map[[2]string]overlapRule{
	// Pair 1, the polarity pair: one new enable_* or attach_* input on the
	// same subject. That it defaults to true is the lead's judgement in both
	// findings.
	{"`compat.default-changed`", "`structure.flag-polarity`"}: {
		sameDefect: func(k, d *item) bool { return flagSubject.MatchString(k.subject) && sameLocus(k, d) },
	},
	// Pair 2: the code's break over the title that misreports it. The row's
	// "finding for the break" excludes the two Check A rules that judge the
	// description, which are never deduplicated against a break. Whether the
	// title misreports that same break is not in the findings, so the merge
	// reads it only where the lead says so by place: the same subject, or the
	// same file and line.
	{"The `compat.*` finding for the break", "`profile.commit-type-mismatch`"}: {
		notKept:    []string{"compat.claim-contradicts-analysis", "compat.break-unexplained"},
		sameDefect: sameLocus,
	},
}

// flagSubject is a subject naming an enable_* or attach_* variable.
var flagSubject = regexp.MustCompile(`:var\.(enable|attach)_[A-Za-z0-9_-]*$`)

// sameLocus reports whether two findings are on the same subject, or, when
// either has none, on the same file and line.
func sameLocus(a, b *item) bool {
	if a.subject != "" && b.subject != "" {
		return a.subject == b.subject
	}
	return a.locus() == b.locus()
}

// dedupStep2 applies the overlap table: a dropped finding goes when a kept
// finding is the same defect, and the kept one takes the higher severity of
// the two, within its rule's list, and keeps its own text.
func (in Inputs) dedupStep2(items []*item) []*item {
	drop := map[*item]bool{}
	for _, o := range in.Registry.Overlaps {
		row := knownOverlaps[[2]string{o.KeptCell, o.DroppedCell}] // in.check refused unknown rows
		for _, d := range items {
			if drop[d] || !registry.Match(o.Dropped, d.f.RuleID) {
				continue
			}
			for _, k := range items {
				if k == d || drop[k] || !registry.Match(o.Kept, k.f.RuleID) ||
					slices.Contains(row.notKept, k.f.RuleID) || !row.sameDefect(k, d) {
					continue
				}
				if registry.SeverityRank(d.f.Severity) < registry.SeverityRank(k.f.Severity) {
					r, _ := in.rule(k.f.RuleID)
					k.f.Severity = clamp(d.f.Severity, r.Severities)
				}
				drop[d] = true
				break
			}
		}
	}
	return slices.DeleteFunc(items, func(it *item) bool { return drop[it] })
}
