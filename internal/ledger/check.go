package ledger

// Problem is one way a ledger fails the inventory.
type Problem struct {
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Field   string `json:"field"` // "" for a problem with the whole type
	Problem string `json:"problem"`
}

// Problem texts.
const (
	ProblemUnmapped        = "unmapped: no disposition"
	ProblemDuplicate       = "duplicate: more than one disposition"
	ProblemUnknownField    = "unknown field: not in the schema"
	ProblemUnusedType      = "unknown type: the module does not declare it"
	ProblemDuplicateType   = "duplicate type"
	ProblemMissingVariable = "input: no such variable in the module"
	ProblemMissingOutput   = "output: no such output in the module"
	ProblemInputComputed   = "input: the field is computed only, so it takes no input"
	ProblemContainer       = "a block or nested attribute with fields takes only intentional_omission or not_applicable; map its fields instead"
)

// coversChildren reports whether a disposition on a field with fields of
// its own covers all of them. Only the two omissions that document a reason
// for the whole field do: an input, an output or a sensitive omission is a
// decision about one value, so each field under it takes its own.
func coversChildren(disp string) bool {
	return disp == DispIntentionalOmission || disp == DispNotApplicable
}

// Check reports every leaf field of the inventory that is not covered exactly
// once, either by its own disposition or by an omission of a block or nested
// attribute above it; every ledger entry the inventory lacks; and every
// input or output the module does not declare or the field cannot take. An
// empty result means the ledger is complete; it says nothing of whether each
// disposition is right.
func Check(inv *Inventory, l *Ledger) []Problem {
	problems := []Problem{}
	byType := map[string]LedgerType{}
	for _, t := range l.Types {
		k := t.Mode + " " + t.Type
		if _, dup := byType[k]; dup {
			problems = append(problems, Problem{Mode: t.Mode, Type: t.Type, Problem: ProblemDuplicateType})
			continue
		}
		byType[k] = t
	}
	used := map[string]bool{}
	for _, it := range inv.Types {
		k := it.Mode + " " + it.Type
		used[k] = true
		problems = append(problems, checkType(inv, it, byType[k].Fields)...)
	}
	for _, t := range l.Types {
		if k := t.Mode + " " + t.Type; !used[k] {
			used[k] = true // one problem per type
			problems = append(problems, Problem{Mode: t.Mode, Type: t.Type, Problem: ProblemUnusedType})
		}
	}
	return problems
}

func checkType(inv *Inventory, it Type, ds []Disposition) []Problem {
	var problems []Problem
	p := func(field, text string) {
		problems = append(problems, Problem{Mode: it.Mode, Type: it.Type, Field: field, Problem: text})
	}
	known := map[string]Field{}
	container := map[string]bool{}
	for _, f := range it.Fields {
		known[f.Name] = f
		container[parent(f.Name)] = true
	}
	count := map[string]int{}  // entries per field
	covers := map[string]int{} // entries per container that cover its fields
	for _, d := range ds {
		count[d.Name]++
		f, ok := known[d.Name]
		switch {
		case !ok:
			if count[d.Name] == 1 {
				p(d.Name, ProblemUnknownField)
			}
		case container[d.Name]:
			if !coversChildren(d.Disposition) {
				p(d.Name, ProblemContainer)
				continue
			}
			covers[d.Name]++
		case d.Disposition == DispInput && f.Computed && !f.Required && !f.Optional:
			p(d.Name, ProblemInputComputed)
		case d.Disposition == DispInput && !inv.variables[d.Variable]:
			p(d.Name, ProblemMissingVariable)
		case d.Disposition == DispOutput && !inv.outputs[d.Output]:
			p(d.Name, ProblemMissingOutput)
		}
	}
	for _, f := range it.Fields {
		n := count[f.Name]
		for a := parent(f.Name); a != ""; a = parent(a) {
			n += covers[a]
		}
		switch {
		case n > 1:
			p(f.Name, ProblemDuplicate)
		case n == 0 && !container[f.Name]:
			p(f.Name, ProblemUnmapped)
		}
	}
	return problems
}
