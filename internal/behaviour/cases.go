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

// cases builds the required sweep of an L1 trigger: one typical case with
// every input in its typical state, then one case per other state of each
// input, the rest typical. It returns no cases and a reason when the sweep
// is over maxSweepCases.
func (c *classifier) cases(t Trigger, dir string) ([]Case, string) {
	var inputs []caseInput
	seen := map[string]bool{}
	addVar := func(name, typ string, omittable bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		inputs = append(inputs, caseInput{name: name, states: varStates(typ, omittable)})
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
		if d.Kind != "resource" && d.Kind != "data" || seen[d.Text] {
			continue
		}
		seen[d.Text] = true
		inputs = append(inputs, caseInput{name: d.Text, states: refStates(d)})
	}
	return sweepCases(inputs)
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

// varStates are the states of a variable of the type constraint typ.
func varStates(typ string, omittable bool) []caseState {
	ty := parseType(typ)
	st := []caseState{{"typical", CaseInput{Value: raw(sample(ty))}}}
	switch {
	case ty.IsListType() || ty.IsSetType():
		el := ty.ElementType()
		st = append(st,
			caseState{"empty", CaseInput{Value: raw([]any{})}},
			caseState{"empty-element", CaseInput{Value: raw([]any{zero(el)})}},
			caseState{"null-element", CaseInput{Value: raw([]any{nil})}},
			caseState{"unknown-element", CaseInput{Value: raw([]any{nil}), UnknownElements: []json.RawMessage{raw(0)}}})
	case ty.IsMapType():
		el := ty.ElementType()
		st = append(st,
			caseState{"empty", CaseInput{Value: raw(map[string]any{})}},
			caseState{"empty-element", CaseInput{Value: raw(map[string]any{"k": zero(el)})}},
			caseState{"null-element", CaseInput{Value: raw(map[string]any{"k": nil})}},
			caseState{"unknown-element", CaseInput{Value: raw(map[string]any{"k": nil}), UnknownElements: []json.RawMessage{raw("k")}}})
	case ty == cty.String || ty == cty.Number || ty == cty.Bool:
		st = append(st, caseState{"zero", CaseInput{Value: raw(zero(ty))}})
	}
	st = append(st, caseState{"unknown", CaseInput{Unknown: true}}, caseState{"null", CaseInput{Null: true}})
	if omittable {
		st = append(st, caseState{"omitted", CaseInput{Omitted: true}})
	}
	return st
}

// refStates are the states of a resource or data source reference: a
// splat is a list of instances, one unknown first, then none and one
// known; any other reference is unknown first, then known.
func refStates(d RefDetail) []caseState {
	el := cty.String
	if d.Type != nil {
		el = parseType(*d.Type)
	}
	if strings.Contains(d.Text, "[*]") {
		return []caseState{
			{"unknown-element", CaseInput{Value: raw([]any{nil}), UnknownElements: []json.RawMessage{raw(0)}}},
			{"empty", CaseInput{Value: raw([]any{})}},
			{"known", CaseInput{Value: raw([]any{sample(el)})}},
		}
	}
	return []caseState{{"unknown", CaseInput{Unknown: true}}, {"known", CaseInput{Value: raw(sample(el))}}}
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
