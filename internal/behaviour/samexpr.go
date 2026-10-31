package behaviour

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// levelOrder is the order of the levels of a merged trigger.
var levelOrder = []string{LevelL1, LevelL2, LevelL3, LevelL4, LevelCompat}

// mergeSameExpr merges the triggers of one file and line whose base and
// head expressions are the same and not null: one change that several
// categories see, verified once. The first keeps its place, id and
// category; the categories of the others go to also_categories, and their
// levels and required cases join the first's. Triggers whose expressions
// differ or are null are never merged, and a merge that would weaken or
// break the sweep is not made: see mergeTwo. ts is sorted.
func mergeSameExpr(ts []Trigger) []Trigger {
	out := make([]Trigger, 0, len(ts))
	at := map[string]int{}
	for _, t := range ts {
		if t.Line == nil || t.BaseExpr == nil || t.HeadExpr == nil {
			out = append(out, t)
			continue
		}
		k := fmt.Sprintf("%s\x00%d\x00%s\x00%s", t.File, *t.Line, *t.BaseExpr, *t.HeadExpr)
		if i, ok := at[k]; !ok {
			at[k] = len(out)
		} else if m, ok := mergeTwo(out[i], t); ok {
			out[i] = m
			continue
		}
		out = append(out, t)
	}
	return out
}

// mergeTwo merges b into a. It refuses when the union of the required
// cases is over maxSweepCases, when one case id has other inputs in each,
// or when one trigger needs required cases it could not get while the
// other has some: the merged trigger would then pass on the other's
// weaker sweep.
func mergeTwo(a, b Trigger) (Trigger, bool) {
	lacks := func(t Trigger) bool {
		return len(t.Cases) == 0 && (slices.Contains(t.Levels, LevelL1) || slices.Contains(t.Levels, LevelL2))
	}
	if lacks(a) != lacks(b) && len(a.Cases)+len(b.Cases) > 0 {
		return a, false
	}
	cases := slices.Clone(a.Cases)
	for _, c := range b.Cases {
		i := slices.IndexFunc(cases, func(o Case) bool { return o.ID == c.ID })
		if i < 0 {
			cases = append(cases, c)
			continue
		}
		x, err1 := json.Marshal(cases[i].Inputs)
		y, err2 := json.Marshal(c.Inputs)
		if err1 != nil || err2 != nil || !jsonEqual(x, y) {
			return a, false
		}
	}
	if len(cases) > maxSweepCases {
		return a, false
	}
	m := a
	if len(cases) > 0 {
		m.Cases = cases
	}
	m.Levels = []string{}
	for _, lv := range levelOrder {
		if slices.Contains(a.Levels, lv) || slices.Contains(b.Levels, lv) {
			m.Levels = append(m.Levels, lv)
		}
	}
	m.AlsoCategories = slices.Clone(a.AlsoCategories)
	for _, c := range append([]string{b.Category}, b.AlsoCategories...) {
		if c != a.Category && !slices.Contains(m.AlsoCategories, c) {
			m.AlsoCategories = append(m.AlsoCategories, c)
		}
	}
	m.Address = cmp.Or(a.Address, b.Address)
	m.Attribute = cmp.Or(a.Attribute, b.Attribute)
	var reasons []string
	for _, r := range strings.Split(deref(a.Reason)+"; "+deref(b.Reason), "; ") {
		if r != "" && !slices.Contains(reasons, r) {
			reasons = append(reasons, r)
		}
	}
	if len(reasons) > 0 {
		joined := strings.Join(reasons, "; ")
		m.Reason = &joined
	}
	m.ForEachOnly = a.ForEachOnly || b.ForEachOnly
	return m, true
}
