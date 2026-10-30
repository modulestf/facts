package behaviour

import (
	"encoding/json"
	"fmt"
	"slices"
)

// maxSweepCases caps the cases of one trigger's sweep, over all its levels.
const maxSweepCases = 32

// Change is one (case, attribute) pair whose observation differs between
// the base and the head, for the reviewer to judge.
type Change struct {
	Level     string `json:"level"`
	Case      string `json:"case"`
	Attribute string `json:"attribute"`
	// Explained is set when verified claims state both sides exactly; it
	// says the change was observed as stated, not that it is compatible.
	Explained bool `json:"explained"`
	// Inputs are the case's inputs, left out when either side is sensitive.
	Inputs map[string]json.RawMessage `json:"inputs"`
}

// casePair is one sweep case at both revisions.
type casePair struct {
	level, id  string
	base, head Observation
}

// sweep decides a trigger that selects L1 or L3 from the tool's own sweep:
// every selected level needs base and head observations from runs with a
// valid control and the same case ids at both revisions. The trigger is
// verified when no (case, attribute) pair differs, or when verified claims
// explain every pair that does. why is set when it is not verified.
func sweep(t Trigger, l *Ladder, claims []Claim, verified map[string]bool) (why string, changes []Change) {
	var pairs []casePair
	for _, lv := range t.Levels {
		if lv != LevelL1 && lv != LevelL3 {
			continue
		}
		var required []string
		if lv == LevelL1 {
			if len(t.Cases) == 0 {
				return "the trigger has no required case for L1", nil
			}
			for _, c := range t.Cases {
				required = append(required, c.ID)
			}
		}
		p, why := levelPairs(t.ID, lv, l, required)
		if why != "" {
			return why, nil
		}
		pairs = append(pairs, p...)
	}
	if len(pairs) == 0 {
		return "the trigger selects no ladder level", nil
	}
	if len(pairs) > maxSweepCases {
		return fmt.Sprintf("the sweep has %d cases, over %d", len(pairs), maxSweepCases), nil
	}
	changes = []Change{}
	for _, p := range pairs {
		attrs, why := differing(p)
		if why != "" {
			return why, nil
		}
		for _, a := range attrs {
			c := Change{Level: p.level, Case: p.id, Attribute: a, Inputs: map[string]json.RawMessage{}}
			if !p.base.Sensitive && !p.head.Sensitive && p.base.Inputs != nil {
				c.Inputs = p.base.Inputs
			}
			c.Explained = explained(p, a, claims, verified)
			changes = append(changes, c)
		}
	}
	for _, c := range changes {
		if !c.Explained {
			return fmt.Sprintf("%s case %q differs in %s and no verified claim states both sides", c.Level, c.Case, c.Attribute), changes
		}
	}
	return "", changes
}

// levelPairs pairs the base and head observations of a level by case id.
// With required ids, each must be observed at both revisions and other
// cases are ignored; without, both revisions must have the same cases.
func levelPairs(id, lv string, l *Ladder, required []string) ([]casePair, string) {
	for _, r := range l.Runs {
		if r.TriggerID == id && r.Level == lv && r.Control != nil && *r.Control == "invalid" {
			return nil, lv + " has a run with an invalid control"
		}
	}
	base, head := map[string]Observation{}, map[string]Observation{}
	for _, r := range validRuns(l, id, []string{lv}) {
		if r.Observations == nil {
			continue
		}
		for _, side := range []struct {
			obs []Observation
			m   map[string]Observation
		}{{r.Observations.Base, base}, {r.Observations.Head, head}} {
			for _, o := range side.obs {
				if _, dup := side.m[o.Case]; dup {
					return nil, fmt.Sprintf("%s case %q is observed twice at one revision", lv, o.Case)
				}
				side.m[o.Case] = o
			}
		}
	}
	if len(base) == 0 || len(head) == 0 {
		return nil, lv + " has no base and head run with a valid control"
	}
	var pairs []casePair
	if required != nil {
		for _, k := range required {
			b, okB := base[k]
			h, okH := head[k]
			switch {
			case !okB:
				return nil, fmt.Sprintf("%s case %q is missing at the base", lv, k)
			case !okH:
				return nil, fmt.Sprintf("%s case %q is missing at the head", lv, k)
			}
			pairs = append(pairs, casePair{level: lv, id: k, base: b, head: h})
		}
		return pairs, ""
	}
	for _, k := range slices.Sorted(keysOfObs(base)) {
		h, ok := head[k]
		if !ok {
			return nil, fmt.Sprintf("%s case %q is missing at the head", lv, k)
		}
		pairs = append(pairs, casePair{level: lv, id: k, base: base[k], head: h})
	}
	for k := range head {
		if _, ok := base[k]; !ok {
			return nil, fmt.Sprintf("%s case %q is missing at the base", lv, k)
		}
	}
	return pairs, ""
}

func keysOfObs(m map[string]Observation) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// differing lists the attributes of a case that differ between the
// revisions. why is set when a value known at both sides cannot be
// compared.
func differing(p casePair) ([]string, string) {
	b, h := p.base, p.head
	var out []string
	add := func(name string, differ bool) {
		if differ {
			out = append(out, name)
		}
	}
	bk, hk := b.Known != nil && *b.Known, h.Known != nil && *h.Known
	add("known", bk != hk)
	add("length", !sameInt(b.Length, h.Length))
	if bk && hk {
		if b.Sensitive || h.Sensitive || b.Redacted || h.Redacted || b.Value == nil || h.Value == nil {
			return nil, fmt.Sprintf("%s case %q is known at both revisions but its value cannot be compared", p.level, p.id)
		}
		add("value", !jsonEqual(b.Value, h.Value))
	}
	add("sensitive", b.Sensitive != h.Sensitive)
	add("redacted", b.Redacted != h.Redacted)
	add("unknown_inputs", !slices.Equal(sortedCopy(b.UnknownInputs), sortedCopy(h.UnknownInputs)))
	add("action", deref(b.Action) != deref(h.Action) || (b.Action == nil) != (h.Action == nil))
	add("unchanged_vs_prior", !sameBool(b.UnchangedVsPrior, h.UnchangedVsPrior))
	return out, ""
}

func sameInt(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameBool(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}

// explained reports whether verified claims state both sides of a pair
// exactly, each on that case's observation at its own revision.
func explained(p casePair, attr string, claims []Claim, verified map[string]bool) bool {
	unknownPred := PredPlanUnknown
	if p.level == LevelL1 {
		unknownPred = PredExprUnknown
	}
	// states reports a verified claim of one of preds at rev that holds on
	// the case's observation at rev.
	states := func(rev string, preds ...string) bool {
		o := p.base
		if rev == "head" {
			o = p.head
		}
		for _, c := range claims {
			if c.Revision != rev || !verified[c.ClaimID] || !slices.Contains(preds, c.PredicateType) ||
				!slices.Contains(predicateLevels[c.PredicateType], p.level) {
				continue
			}
			if judge(c, o) != vSupport {
				continue
			}
			if c.PredicateType == PredExprLengthKnown && (c.Length == nil || o.Length == nil || *c.Length != *o.Length) {
				continue
			}
			return true
		}
		return false
	}
	both := func(preds ...string) bool { return states("base", preds...) && states("head", preds...) }
	side := func(rev string, unknown bool, nullLength bool) bool {
		switch attr {
		case "known":
			switch {
			case unknown:
				return states(rev, unknownPred)
			case p.level == LevelL1:
				return states(rev, PredExprEquals)
			}
			// A plan shows a known value as unchanged against the prior state.
			return states(rev, PredPlanUnchanged)
		case "length":
			if nullLength {
				return states(rev, PredExprUnknown)
			}
			return states(rev, PredExprLengthKnown, PredExprEquals)
		}
		return false
	}
	switch attr {
	case "known":
		return side("base", !known(p.base), false) && side("head", !known(p.head), false)
	case "length":
		return side("base", false, p.base.Length == nil) && side("head", false, p.head.Length == nil)
	case "value":
		return both(PredExprEquals)
	case "action":
		return both(PredPlanAction)
	case "unchanged_vs_prior":
		pick := func(rev string, o Observation) bool {
			if o.UnchangedVsPrior != nil && *o.UnchangedVsPrior {
				return states(rev, PredPlanUnchanged)
			}
			return states(rev, PredPlanUnknown)
		}
		return pick("base", p.base) && pick("head", p.head)
	}
	return false // sensitive, redacted and unknown_inputs are never explained
}

func known(o Observation) bool { return o.Known != nil && *o.Known }
