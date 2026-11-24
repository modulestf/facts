package behaviour

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// toggleShape is a module whose local picks a description from a list of
// opt-in toggles, as a policy module does: coalesce over var.desc and one
// `var.tNN ? "Text NN" : null` argument per toggle. The head adds the
// toggle var.t_new, default false, with its argument at position insertAt.
// newArg and newVar replace the head's added argument and variable block
// when set.
type toggleShape struct {
	toggles, insertAt int
	newArg, newVar    string
}

func (s toggleShape) trees() (base, head map[string]string) {
	vars := "variable \"desc\" {\n  type    = string\n  default = null\n}\n"
	var args []string
	for i := range s.toggles {
		vars += fmt.Sprintf("variable \"t%02d\" {\n  type    = bool\n  default = false\n}\n", i)
		args = append(args, fmt.Sprintf("    var.t%02d ? \"Text %02d\" : null,\n", i, i))
	}
	local := func(args []string) string {
		return "locals {\n  description = try(coalesce(\n    var.desc,\n" + strings.Join(args, "") + "  ), null)\n}\n"
	}
	newArg := cmpOr(s.newArg, "var.t_new ? \"Text new\" : null")
	newVar := cmpOr(s.newVar, "variable \"t_new\" {\n  type    = bool\n  default = false\n}\n")
	headArgs := slices.Insert(slices.Clone(args), s.insertAt, "    "+newArg+",\n")
	return map[string]string{"variables.tf": vars, "main.tf": local(args)},
		map[string]string{"variables.tf": vars + newVar, "main.tf": local(headArgs)}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func descTrigger(t *testing.T, r *Report) Trigger {
	t.Helper()
	for _, tr := range r.Triggers {
		if str(tr.Address) == "local.description" {
			return tr
		}
	}
	t.Fatalf("no trigger for local.description: %s", mustJSON(t, r))
	return Trigger{}
}

// TestAddedToggleNoop: a toggle a change adds to a coalesce list, off by
// default, leaves the value unchanged for every caller that exists at the
// base. Its use site gets a sweep that fits the cap, with the toggle
// pinned to its default in every case, though the expressions hold more
// than 8 string literals and 17 inputs; the added variable follows it.
func TestAddedToggleNoop(t *testing.T) {
	base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
	r := runPair(t, base, head, "")
	tr := descTrigger(t, r)
	if len(tr.Cases) == 0 || len(tr.Cases) > maxSweepCases {
		t.Fatalf("%d cases: %s", len(tr.Cases), mustJSON(t, tr))
	}
	if str(tr.Reason) != reasonNoop+"; "+reasonSmoke {
		t.Errorf("reason: %s", str(tr.Reason))
	}
	states := map[string]map[string]bool{}
	for _, c := range tr.Cases {
		if in := c.Inputs["t_new"]; string(in.Value) != "false" {
			t.Errorf("case %s: t_new is %s, want its default false", c.ID, mustJSON(t, in))
		}
		for name, in := range c.Inputs {
			if states[name] == nil {
				states[name] = map[string]bool{}
			}
			states[name][string(mustJSON(t, in))] = true
		}
	}
	// Every old input still takes more than one state.
	for _, name := range []string{"desc", "t00", "t15"} {
		if len(states[name]) < 2 {
			t.Errorf("%s takes %d states", name, len(states[name]))
		}
	}
	for _, f := range r.Triggers {
		if str(f.Address) == "var.t_new" && !slices.Contains(f.Follows, tr.ID) {
			t.Errorf("var.t_new does not follow %s: %s", tr.ID, mustJSON(t, f))
		}
	}
}

// TestAddedToggleNoopRefused: the proof holds only for the exact shape;
// anything else keeps the plain sweep and its refusal.
func TestAddedToggleNoopRefused(t *testing.T) {
	cases := map[string]toggleShape{
		"empty string not null": {toggles: 16, insertAt: 3, newArg: `var.t_new ? "Text new" : ""`},
		"default true":          {toggles: 16, insertAt: 3, newVar: "variable \"t_new\" {\n  type    = bool\n  default = true\n}\n"},
		"not nullable": {toggles: 16, insertAt: 3,
			newVar: "variable \"t_new\" {\n  type     = bool\n  default  = false\n  nullable = false\n}\n"},
		"validated": {toggles: 16, insertAt: 3,
			newVar: "variable \"t_new\" {\n  type    = bool\n  default = false\n  validation {\n    condition     = true\n    error_message = \"x\"\n  }\n}\n"},
		"read twice":               {toggles: 16, insertAt: 3, newArg: `var.t_new ? "Text new" : null, var.t_new ? "Again" : "x"`},
		"negated":                  {toggles: 16, insertAt: 3, newArg: `!var.t_new ? "Text new" : null`},
		"number result":            {toggles: 16, insertAt: 3, newArg: `var.t_new ? 1 : null`},
		"no other string argument": {toggles: 0, insertAt: 0},
		"old toggle var":           {toggles: 16, insertAt: 3, newArg: `var.t00 ? "Text new" : null`, newVar: "\n"},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			base, head := s.trees()
			tr := descTrigger(t, runPair(t, base, head, ""))
			// With few inputs the plain sweep fits; it must not be the proof.
			if s.toggles > 0 && len(tr.Cases) != 0 || strings.Contains(str(tr.Reason), "exists at the base") {
				t.Errorf("%d cases, reason %q", len(tr.Cases), str(tr.Reason))
			}
		})
	}
	// A for expression that names its iterator var shadows the variables.
	base, head := toggleShape{toggles: 16, insertAt: 3}.trees()
	for _, m := range []map[string]string{base, head} {
		m["main.tf"] = strings.Replace(m["main.tf"], "    var.desc,\n", "    var.desc,\n    [for var in [{ a = \"x\" }] : var.a][0],\n", 1)
	}
	if tr := descTrigger(t, runPair(t, base, head, "")); len(tr.Cases) != 0 || strings.Contains(str(tr.Reason), "exists at the base") {
		t.Errorf("shadowed var: %d cases, reason %q", len(tr.Cases), str(tr.Reason))
	}
	// Another argument changed as well: the head is not the base without
	// the added argument.
	base, head = toggleShape{toggles: 16, insertAt: 3}.trees()
	head["main.tf"] = strings.Replace(head["main.tf"], `"Text 07"`, `"Text seven"`, 1)
	if tr := descTrigger(t, runPair(t, base, head, "")); len(tr.Cases) != 0 {
		t.Errorf("other argument changed: %d cases", len(tr.Cases))
	}
}

// TestAddedToggleNoopOverCap: with the proof, one case per input state
// still has to fit the cap; past it the trigger has no cases and a reason.
func TestAddedToggleNoopOverCap(t *testing.T) {
	base, head := toggleShape{toggles: maxSweepCases, insertAt: 5}.trees()
	tr := descTrigger(t, runPair(t, base, head, ""))
	if len(tr.Cases) != 0 || !strings.Contains(str(tr.Reason), "cap") {
		t.Errorf("%d cases, reason %q", len(tr.Cases), str(tr.Reason))
	}
}

// TestAddedToggleNoopPremise: no caller at the base can set the toggle only
// when the walk read every file that may declare, override or set it, so a
// directory with a .tf.json, .tofu or .tofu.json file or a variable
// definitions file at either revision keeps the plain sweep and its refusal.
func TestAddedToggleNoopPremise(t *testing.T) {
	cases := map[string]struct{ base, head map[string]string }{
		"head override.tf.json":  {head: map[string]string{"override.tf.json": `{"variable":{"t_new":{"default":true}}}`}},
		"head .tofu":             {head: map[string]string{"variables.tofu": "variable \"t_new\" {\n  default = true\n}\n"}},
		"head .tofu.json":        {head: map[string]string{"x.tofu.json": "{}"}},
		"base .tf.json declares": {base: map[string]string{"extra.tf.json": `{"variable":{"t_new":{"type":"bool"}}}`}},
		"head terraform.tfvars":  {head: map[string]string{"terraform.tfvars": "t_new = true\n"}},
		"base auto.tfvars.json":  {base: map[string]string{"x.auto.tfvars.json": `{"t_new": true}`}},
		"head upper-case TFVARS": {head: map[string]string{"Prod.TFVARS": "t_new = true\n"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
			maps.Copy(base, c.base)
			maps.Copy(head, c.head)
			tr := descTrigger(t, runPair(t, base, head, ""))
			if len(tr.Cases) != 0 || strings.Contains(str(tr.Reason), "exists at the base") {
				t.Errorf("%d cases, reason %q", len(tr.Cases), str(tr.Reason))
			}
		})
	}
	// A variable definitions file in another directory does not matter.
	base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
	head["examples/x/terraform.tfvars"] = "t_new = true\n"
	if tr := descTrigger(t, runPair(t, base, head, "")); len(tr.Cases) == 0 {
		t.Errorf("tfvars in another directory: %s", str(tr.Reason))
	}
}
