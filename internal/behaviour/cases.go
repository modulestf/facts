package behaviour

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Case is one required case of a trigger's L1 sweep: a value for every
// input, so the evaluation sees the same inputs at base and head.
type Case struct {
	ID     string               `json:"id"`
	Inputs map[string]CaseInput `json:"inputs"`
}

// CaseInput is the value of one input in a case. Exactly one of Value,
// Unknown, Null and Omitted is set. An input is a variable by name, or a
// contextual reference by its text in contextual_ref_details.
type CaseInput struct {
	// Value is a known value of the input's type. With UnknownElements set,
	// those elements of it are unknown and hold null here.
	Value json.RawMessage `json:"value"`
	// Unknown is a wholly unknown value of the input's type.
	Unknown bool `json:"unknown"`
	Null    bool `json:"null"`
	// Omitted leaves a variable unset, so its default at each revision
	// applies; only for the variable a default change is about.
	Omitted bool `json:"omitted"`
	// UnknownElements are the list indexes or map keys of Value that are
	// unknown.
	UnknownElements []json.RawMessage `json:"unknown_elements,omitempty"`
}

// caseInput is one input with its states, the first being its typical one.
type caseInput struct {
	name   string
	states []caseState
}

type caseState struct {
	label string
	in    CaseInput
}

var caseIDBad = regexp.MustCompile(`[^A-Za-z0-9_.:-]`)

// maxLiterals caps the distinct literals of one kind that become states.
const maxLiterals = 8

// cases builds the required sweep of an L1 trigger from its inputs: its
// variables, its resource, data, count and each references, and for a
// default or type change the variable itself. Every string, number and bool
// literal of either expression is an extra state of each input of a
// matching type. It returns no cases and a reason when an input has no
// states, as a local, module or self reference, or the sweep is over
// maxSweepCases.
func (c *classifier) cases(t Trigger, dir string, xs ...*exprInfo) ([]Case, string) {
	lits, why := literals(xs...)
	if why != "" {
		return []Case{}, why
	}
	var inputs []caseInput
	seen := map[string]bool{}
	addVar := func(name, typ string, omittable bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		inputs = append(inputs, caseInput{name: name, states: varStates(parseType(typ), omittable, lits)})
	}
	if t.Address != nil && (t.Category == CatDefault || t.Category == CatTypeValidation) {
		if name, ok := strings.CutPrefix(*t.Address, "var."); ok {
			typ := "any"
			if v := c.views[dir]; v[1] != nil && v[1].vars[name] != "" {
				typ = v[1].vars[name]
			} else if v[0] != nil && v[0].vars[name] != "" {
				typ = v[0].vars[name]
			}
			addVar(name, typ, t.Category == CatDefault)
		}
	}
	for _, in := range t.ReferencedInputs {
		addVar(in.Name, in.Type, false)
	}
	for _, d := range t.ContextualRefDetails {
		if seen[d.Text] {
			continue
		}
		seen[d.Text] = true
		st, why := refStates(d, lits)
		if why != "" {
			return []Case{}, why
		}
		if st != nil {
			inputs = append(inputs, caseInput{name: d.Text, states: st})
		}
	}
	return sweepCases(inputs)
}

// literalSet holds the distinct literals of the expressions, by kind.
type literalSet struct {
	strings []string
	numbers []string // as written, canonical JSON numbers
	bools   []bool
}

// literals collects the string, number and bool literals of expressions.
func literals(xs ...*exprInfo) (literalSet, string) {
	var ls literalSet
	strs, nums := map[string]bool{}, map[string]bool{}
	for _, x := range xs {
		if x == nil {
			continue
		}
		_ = hclsyntax.VisitAll(x.expr, func(n hclsyntax.Node) hcl.Diagnostics {
			lit, ok := n.(*hclsyntax.LiteralValueExpr)
			if !ok || lit.Val.IsNull() || !lit.Val.IsKnown() {
				return nil
			}
			switch lit.Val.Type() {
			case cty.String:
				strs[lit.Val.AsString()] = true
			case cty.Number:
				nums[lit.Val.AsBigFloat().Text('g', -1)] = true
			case cty.Bool:
				if b := lit.Val.True(); !slices.Contains(ls.bools, b) {
					ls.bools = append(ls.bools, b)
				}
			}
			return nil
		})
	}
	ls.strings, ls.numbers = slices.Sorted(maps.Keys(strs)), slices.Sorted(maps.Keys(nums))
	if len(ls.strings) > maxLiterals || len(ls.numbers) > maxLiterals {
		return ls, fmt.Sprintf("the expressions have more than %d string or number literals", maxLiterals)
	}
	return ls, ""
}

// literalStates are the extra states of a scalar of type ty: each literal
// of its kind that is not already a state, wrapped by wrap.
func literalStates(ty cty.Type, ls literalSet, have []caseState, wrap func(any) any) []caseState {
	type lit struct {
		label string
		v     any
	}
	var vals []lit
	switch ty {
	case cty.String:
		for i, s := range ls.strings {
			vals = append(vals, lit{fmt.Sprintf("str%d", i), s})
		}
	case cty.Number:
		for i, n := range ls.numbers {
			vals = append(vals, lit{fmt.Sprintf("num%d", i), json.Number(n)})
		}
	case cty.Bool:
		for i, b := range ls.bools {
			vals = append(vals, lit{fmt.Sprintf("bool%d", i), b})
		}
	case cty.DynamicPseudoType:
		return append(literalStates(cty.String, ls, have, wrap), literalStates(cty.Number, ls, have, wrap)...)
	}
	var out []caseState
	for _, l := range vals {
		in := CaseInput{Value: raw(wrap(l.v))}
		if slices.ContainsFunc(have, func(st caseState) bool { return st.in.Value != nil && jsonEqual(st.in.Value, in.Value) }) {
			continue
		}
		out = append(out, caseState{l.label, in})
	}
	return out
}

// sweepCases combines the input states: every combination when there are
// at most maxSweepCases, else one typical case and one case per other state
// of each input with the rest typical, else none and a reason.
func sweepCases(inputs []caseInput) ([]Case, string) {
	product := 1
	oneAtATime := 1
	for _, in := range inputs {
		product *= len(in.states)
		oneAtATime += len(in.states) - 1
		if product > maxSweepCases {
			product = maxSweepCases + 1 // no overflow; only the cap matters
		}
	}
	if len(inputs) == 0 {
		return []Case{{ID: "typical", Inputs: map[string]CaseInput{}}}, ""
	}
	if product <= maxSweepCases {
		return productCases(inputs), ""
	}
	if oneAtATime > maxSweepCases {
		return []Case{}, fmt.Sprintf("the input sweep needs %d cases, over the cap of %d", oneAtATime, maxSweepCases)
	}
	typical := func() map[string]CaseInput {
		m := map[string]CaseInput{}
		for _, in := range inputs {
			m[in.name] = in.states[0].in
		}
		return m
	}
	out := []Case{{ID: "typical", Inputs: typical()}}
	used := map[string]bool{"typical": true}
	for _, in := range inputs {
		for _, st := range in.states[1:] {
			m := typical()
			m[in.name] = st.in
			id := caseID(in.name, st.label)
			for i := 2; used[id]; i++ {
				id = caseID(in.name, fmt.Sprintf("%s-%d", st.label, i))
			}
			used[id] = true
			out = append(out, Case{ID: id, Inputs: m})
		}
	}
	return out, ""
}

// productCases lists every combination of the input states, in input
// order. A case id joins the state labels with dots, or is c-NN when that
// is too long.
func productCases(inputs []caseInput) []Case {
	out := []Case{{Inputs: map[string]CaseInput{}}}
	labels := [][]string{nil}
	for _, in := range inputs {
		var next []Case
		var nextLabels [][]string
		for i, c := range out {
			for _, st := range in.states {
				m := maps.Clone(c.Inputs)
				m[in.name] = st.in
				next = append(next, Case{Inputs: m})
				nextLabels = append(nextLabels, append(slices.Clone(labels[i]), st.label))
			}
		}
		out, labels = next, nextLabels
	}
	for i := range out {
		out[i].ID = strings.Join(labels[i], ".")
		if len(out[i].ID) > 64 {
			out[i].ID = fmt.Sprintf("c-%02d", i+1)
		}
	}
	return out
}

// caseID is "<input>:<state>", with a splat's [*] dropped, other characters
// outside the id pattern replaced and the input cut so the id fits.
func caseID(name, label string) string {
	name = caseIDBad.ReplaceAllString(strings.ReplaceAll(name, "[*]", ""), "_")
	if limit := 64 - len(label) - 1; len(name) > limit {
		name = name[:limit]
	}
	return name + ":" + label
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// varStates are the states of a variable of type ty.
func varStates(ty cty.Type, omittable bool, lits literalSet) []caseState {
	st := append([]caseState{{"typical", CaseInput{Value: raw(sample(ty))}}}, shapeStates(ty, lits)...)
	st = append(st, caseState{"unknown", CaseInput{Unknown: true}}, caseState{"null", CaseInput{Null: true}})
	if omittable {
		st = append(st, caseState{"omitted", CaseInput{Omitted: true}})
	}
	return st
}

// shapeStates are the known states of a type beside its typical sample:
// for a collection none, an empty element, a null element, an unknown
// element and one element per literal; for a scalar zero and the literals.
func shapeStates(ty cty.Type, lits literalSet) []caseState {
	var st []caseState
	switch {
	case ty.IsListType() || ty.IsSetType():
		el := ty.ElementType()
		st = []caseState{
			{"empty", CaseInput{Value: raw([]any{})}},
			{"empty-element", CaseInput{Value: raw([]any{zero(el)})}},
			{"null-element", CaseInput{Value: raw([]any{nil})}},
			{"unknown-element", CaseInput{Value: raw([]any{nil}), UnknownElements: []json.RawMessage{raw(0)}}},
		}
		st = append(st, literalStates(el, lits, append(st, caseState{in: CaseInput{Value: raw([]any{sample(el)})}}), func(v any) any { return []any{v} })...)
	case ty.IsMapType():
		el := ty.ElementType()
		st = []caseState{
			{"empty", CaseInput{Value: raw(map[string]any{})}},
			{"empty-element", CaseInput{Value: raw(map[string]any{"k": zero(el)})}},
			{"null-element", CaseInput{Value: raw(map[string]any{"k": nil})}},
			{"unknown-element", CaseInput{Value: raw(map[string]any{"k": nil}), UnknownElements: []json.RawMessage{raw("k")}}},
		}
		st = append(st, literalStates(el, lits, append(st, caseState{in: CaseInput{Value: raw(map[string]any{"k": sample(el)})}}), func(v any) any { return map[string]any{"k": v} })...)
	case ty == cty.String || ty == cty.Number || ty == cty.Bool || ty == cty.DynamicPseudoType:
		st = []caseState{{"zero", CaseInput{Value: raw(zero(ty))}}}
		st = append(st, literalStates(ty, lits, append(st, caseState{in: CaseInput{Value: raw(sample(ty))}}), func(v any) any { return v })...)
	}
	return st
}

// refStates are the states of a contextual reference. A resource or data
// splat is a list of instances: one unknown instance first, then none, one
// with a null attribute and one known. Any other resource or data reference
// is unknown first, then known, with the states of a collection of its type
// and the literals. count.index is 0 or 1 and each.key "a" or "". path and
// terraform references are fixed and are no input (nil, ""). A local,
// module, self or each.value reference has no states: why says so.
func refStates(d RefDetail, lits literalSet) ([]caseState, string) {
	switch {
	case d.Kind == "path" || d.Kind == "terraform":
		return nil, ""
	case d.Text == "count.index":
		return []caseState{{"index0", CaseInput{Value: raw(0)}}, {"index1", CaseInput{Value: raw(1)}}}, ""
	case d.Text == "each.key":
		return []caseState{{"key", CaseInput{Value: raw("a")}}, {"empty-key", CaseInput{Value: raw("")}}}, ""
	case d.Kind != "resource" && d.Kind != "data":
		return nil, fmt.Sprintf("a reference to %s has no input states, so the L1 sweep cannot stand in for it", d.Text)
	}
	el := cty.String
	if d.Type != nil {
		el = parseType(*d.Type)
	}
	if strings.Contains(d.Text, "[*]") {
		st := []caseState{
			{"unknown-element", CaseInput{Value: raw([]any{nil}), UnknownElements: []json.RawMessage{raw(0)}}},
			{"empty", CaseInput{Value: raw([]any{})}},
			{"null-element", CaseInput{Value: raw([]any{nil})}},
			{"known", CaseInput{Value: raw([]any{sample(el)})}},
		}
		return append(st, literalStates(el, lits, st, func(v any) any { return []any{v} })...), ""
	}
	st := []caseState{{"unknown", CaseInput{Unknown: true}}, {"known", CaseInput{Value: raw(sample(el))}}}
	return append(st, shapeStates(el, lits)...), ""
}

// parseType parses a type constraint; anything it cannot parse, such as a
// type with optional() attributes, is any.
func parseType(s string) cty.Type {
	e, diags := hclsyntax.ParseExpression([]byte(s), "", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.DynamicPseudoType
	}
	ty, diags := typeexpr.TypeConstraint(e)
	if diags.HasErrors() {
		return cty.DynamicPseudoType
	}
	return ty
}

// sample is a typical known value of a type.
func sample(ty cty.Type) any {
	switch {
	case ty == cty.String, ty == cty.DynamicPseudoType:
		return "a"
	case ty == cty.Number:
		return 1
	case ty == cty.Bool:
		return true
	case ty.IsListType(), ty.IsSetType():
		return []any{sample(ty.ElementType())}
	case ty.IsMapType():
		return map[string]any{"k": sample(ty.ElementType())}
	case ty.IsObjectType():
		m := map[string]any{}
		for _, name := range slices.Sorted(maps.Keys(ty.AttributeTypes())) {
			m[name] = sample(ty.AttributeType(name))
		}
		return m
	case ty.IsTupleType():
		var out []any
		for _, et := range ty.TupleElementTypes() {
			out = append(out, sample(et))
		}
		return out
	}
	return "a"
}

// zero is the empty value of a type: "", 0, false or an empty collection.
func zero(ty cty.Type) any {
	switch {
	case ty == cty.Number:
		return 0
	case ty == cty.Bool:
		return false
	case ty.IsListType(), ty.IsSetType(), ty.IsTupleType():
		return []any{}
	case ty.IsMapType(), ty.IsObjectType():
		return map[string]any{}
	}
	return ""
}
