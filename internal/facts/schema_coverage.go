package facts

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/schema"
)

// SchemaCoverage is one element of the facts bundle's schema_coverage
// section: a resource or data block the change adds or edits, the outcome
// of its type's lookup at the latest version, and the argument and nested
// block paths the change adds or removes with their state in the latest
// and declared-minimum schemas.
type SchemaCoverage struct {
	File           string             `json:"file"`
	Line           int                `json:"line"`
	Address        string             `json:"address"`
	ProviderSource string             `json:"provider_source"`
	Outcome        string             `json:"outcome"`
	Arguments      []CoverageArgument `json:"arguments"`
}

// CoverageArgument is one path a block's change adds or removes.
type CoverageArgument struct {
	Path              string `json:"path"`
	Change            string `json:"change"`
	AtLatest          string `json:"at_latest"`
	AtDeclaredMinimum string `json:"at_declared_minimum"`
}

// BuildSchemaCoverage lists the coverage of every resource and data block
// the change adds or edits, sorted by file and line. It is nil unless the
// run has a schema handoff bound to its commits and trees, and leaves out
// an area whose revisions do not both parse in full, as the interface diff
// does. A removed block is not listed: the rules that read the section
// judge blocks the change adds or edits.
func BuildSchemaCoverage(areas []*Area) *[]SchemaCoverage {
	if len(areas) == 0 || areas[0].Schema == nil || areas[0].Schema.Handoff == nil || areas[0].Schema.Mismatch != "" {
		return nil
	}
	out := []SchemaCoverage{}
	for _, a := range areas {
		if a.Untouched || a.Issue != "" || len(a.Head.Unsupported) > 0 || (a.Base != nil && len(a.Base.Unsupported) > 0) {
			continue
		}
		for _, r := range a.Head.Resources {
			base := baseResource(a, r)
			if base != nil && base.Hash == r.Hash {
				continue // untouched
			}
			out = append(out, blockCoverage(a, r, base))
		}
	}
	slices.SortStableFunc(out, func(x, y SchemaCoverage) int {
		return cmp.Or(strings.Compare(x.File, y.File), cmp.Compare(x.Line, y.Line))
	})
	return &out
}

// blockCoverage is the coverage of one touched block. A provider the
// handoff does not name keeps its local name as the source.
func blockCoverage(a *Area, r Resource, base *Resource) SchemaCoverage {
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	c := SchemaCoverage{File: areaFile(a.Path, r.File), Line: r.Line, Address: coverageBlockAddr(r),
		Outcome: string(schema.Unavailable), Arguments: []CoverageArgument{}}
	res := a.Schema.Resolve(a.Path, local)
	c.ProviderSource = cmp.Or(res.Source, local)
	if res.Outcome != schema.Present {
		return c
	}
	ps := providerSchema{set: a.Schema, source: res.Source, latest: res.Versions.Latest, minimum: res.Versions.DeclaredMinimum,
		data: r.Mode == "data", typ: r.Type}
	o, _ := ps.lookup(ps.latest, nil)
	if c.Outcome = string(o); o != schema.Present {
		return c
	}
	head := coveragePaths(ps, r)
	var had map[string]schemaItem
	if base != nil {
		had = coveragePaths(ps, *base)
	}
	for _, p := range slices.Sorted(maps.Keys(head)) {
		if _, ok := had[p]; !ok {
			c.Arguments = append(c.Arguments, coverageArgument(p, "added", head[p]))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(had)) {
		if _, ok := head[p]; !ok {
			c.Arguments = append(c.Arguments, coverageArgument(p, "removed", had[p]))
		}
	}
	return c
}

// coveragePaths maps each schema path a block sets to its first item. The
// type itself, with an empty path, is the block's outcome, not a path.
func coveragePaths(ps providerSchema, r Resource) map[string]schemaItem {
	m := map[string]schemaItem{}
	for _, it := range collectItems(ps, coverageBlockAddr(r), r.File, r.Body, nil, true) {
		if p := strings.Join(it.path, "."); p != "" {
			if _, ok := m[p]; !ok {
				m[p] = it
			}
		}
	}
	return m
}

func coverageArgument(p, change string, it schemaItem) CoverageArgument {
	return CoverageArgument{Path: p, Change: change, AtLatest: factState(it.latest, it.field), AtDeclaredMinimum: factState(it.minimum, it.minimal)}
}
