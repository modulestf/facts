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
			if s.toggles > 0 && len(tr.Cases) != 0 || proven(tr) {
				t.Errorf("%d cases, reason %q", len(tr.Cases), str(tr.Reason))
			}
		})
	}
	// A for expression that names its iterator var shadows the variables.
	base, head := toggleShape{toggles: 16, insertAt: 3}.trees()
	for _, m := range []map[string]string{base, head} {
		m["main.tf"] = strings.Replace(m["main.tf"], "    var.desc,\n", "    var.desc,\n    [for var in [{ a = \"x\" }] : var.a][0],\n", 1)
	}
	if tr := descTrigger(t, runPair(t, base, head, "")); len(tr.Cases) != 0 || proven(tr) {
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
			if len(tr.Cases) != 0 || proven(tr) {
				t.Errorf("%d cases, reason %q", len(tr.Cases), str(tr.Reason))
			}
		})
	}
	// Below the module directory, or anywhere in the tree for a test file
	// or a Terragrunt configuration that names the toggle: refused. A
	// variable definitions file in a sibling directory and a Terragrunt
	// file that does not name it do not matter.
	for name, c := range map[string]struct {
		files map[string]string
		proof bool
	}{
		"tfvars below":           {map[string]string{"modules/m/env/prod.tfvars": "t_new = true\n"}, false},
		"tftest names it":        {map[string]string{"tests/x.tftest.hcl": "variables {\n  t_new = true\n}\n"}, false},
		"terragrunt names it":    {map[string]string{"live/terragrunt.hcl": "inputs = {\n  t_new = true\n}\n"}, false},
		"sibling tfvars":         {map[string]string{"examples/x/terraform.tfvars": "t_new = true\n"}, true},
		"terragrunt other names": {map[string]string{"live/terragrunt.hcl": "inputs = {\n  t_newer = true\n}\n"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
			base, head = under("modules/m", base), under("modules/m", head)
			maps.Copy(base, c.files)
			maps.Copy(head, c.files)
			if tr := descTrigger(t, runPair(t, base, head, "")); proven(tr) != c.proof || c.proof == (len(tr.Cases) == 0) {
				t.Errorf("proof %v, want %v: %d cases, %s", proven(tr), c.proof, len(tr.Cases), str(tr.Reason))
			}
		})
	}
}

// under puts the files of a tree under dir.
func under(dir string, files map[string]string) map[string]string {
	out := map[string]string{}
	for p, body := range files {
		out[dir+"/"+p] = body
	}
	return out
}

func proven(tr Trigger) bool { return strings.Contains(str(tr.Reason), reasonNoop) }

// TestAddedToggleNoopReads: a dropped argument that reads a resource, data
// source, module call or local the base expression does not read gives the
// block a new dependency, so it gets no proof; one the base already reads
// keeps it.
func TestAddedToggleNoopReads(t *testing.T) {
	res := "resource \"aws_kms_key\" \"k\" {}\nresource \"aws_kms_key\" \"j\" {}\n"
	for arg, want := range map[string]bool{
		`var.t_new ? "key ${aws_kms_key.k.arn}" : null`:       false,
		`var.t_new ? "key ${data.aws_region.r.name}" : null`:  false,
		`var.t_new ? "key ${local.other}" : null`:             false,
		`var.t_new ? "key ${module.m.out}" : null`:            false,
		`var.t_new ? "key ${aws_kms_key.j.arn}" : null`:       true,
		`var.t_new ? "key ${var.desc} ${path.module}" : null`: true,
	} {
		t.Run(arg, func(t *testing.T) {
			base, head := toggleShape{toggles: 16, insertAt: 12, newArg: arg}.trees()
			for _, m := range []map[string]string{base, head} {
				m["res.tf"] = res
				m["main.tf"] = strings.Replace(m["main.tf"], "    var.desc,\n", "    var.desc,\n    var.t00 ? aws_kms_key.j.arn : null,\n", 1)
			}
			if tr := descTrigger(t, runPair(t, base, head, "")); proven(tr) != want {
				t.Errorf("proof %v, want %v: %s", proven(tr), want, str(tr.Reason))
			}
		})
	}
}

// TestAddedToggleNoopCallSets: a module call of the head that sets the
// toggle makes an existing caller pass it, so no proof; the generated
// wrapper line, which passes on only what its caller passes, keeps it.
func TestAddedToggleNoopCallSets(t *testing.T) {
	for value, want := range map[string]bool{
		"true": false,
		"try(each.value.t_new, var.defaults.t_new, false)": true,
	} {
		t.Run(value, func(t *testing.T) {
			base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
			base, head = under("modules/m", base), under("modules/m", head)
			call := "module \"m\" {\n  source = \"../modules/m\"\n%s}\n"
			base["root/main.tf"] = fmt.Sprintf(call, "")
			head["root/main.tf"] = fmt.Sprintf(call, "  t_new  = "+value+"\n")
			if tr := descTrigger(t, runPair(t, base, head, "")); proven(tr) != want {
				t.Errorf("proof %v, want %v: %s", proven(tr), want, str(tr.Reason))
			}
		})
	}
}

// TestAddedToggleNoopFallback: past the cap the cases start from every
// input off, so each old toggle is reached alone with the description
// null.
func TestAddedToggleNoopFallback(t *testing.T) {
	base, head := toggleShape{toggles: 16, insertAt: 12}.trees()
	tr := descTrigger(t, runPair(t, base, head, ""))
	for i := range 16 {
		name := fmt.Sprintf("t%02d", i)
		alone := slices.ContainsFunc(tr.Cases, func(c Case) bool {
			if !c.Inputs["desc"].Null || string(c.Inputs[name].Value) != "true" {
				return false
			}
			for n, in := range c.Inputs {
				if n != name && n != "desc" && string(in.Value) != "false" {
					return false
				}
			}
			return true
		})
		if !alone {
			t.Errorf("no case with only %s on: %s", name, mustJSON(t, tr.Cases))
		}
	}
}

// TestAddedToggleNoopScope: the proof never applies to a trigger that
// selects L2 or a dynamic block, nor to a true result that is not a
// template or a call that expands its last argument; it applies to a
// resource argument, and to the trigger the same line merges into it, as
// to a local; an argument appended last to a call with no trailing comma
// is the base too.
func TestAddedToggleNoopScope(t *testing.T) {
	vars := "variable \"desc\" {\n  type    = string\n  default = null\n}\nvariable \"t00\" {\n  type    = bool\n  default = false\n}\n"
	newVar := "variable \"t_new\" {\n  type    = bool\n  default = false\n}\n"
	co := func(extra string) string {
		return `try(coalesce(var.desc, var.t00 ? "Text 00" : null` + extra + `), null)`
	}
	added := `, var.t_new ? "Text new" : null`
	for name, c := range map[string]struct {
		base, head string
		proof      bool
	}{
		"local appended last": {"locals {\n  description = " + co("") + "\n}\n", "locals {\n  description = " + co(added) + "\n}\n", true},
		"resource argument": {"resource \"aws_iam_role\" \"r\" {\n  description = " + co("") + "\n}\n",
			"resource \"aws_iam_role\" \"r\" {\n  description = " + co(added) + "\n}\n", true},
		"count selects L2": {"resource \"aws_iam_role\" \"r\" {\n  count = length(" + co("") + ") > 0 ? 1 : 0\n}\n",
			"resource \"aws_iam_role\" \"r\" {\n  count = length(" + co(added) + ") > 0 ? 1 : 0\n}\n", false},
		"dynamic for_each": {"resource \"aws_iam_role\" \"r\" {\n  dynamic \"d\" {\n    for_each = " + co("") + " == null ? [] : [1]\n    content {}\n  }\n}\n",
			"resource \"aws_iam_role\" \"r\" {\n  dynamic \"d\" {\n    for_each = " + co(added) + " == null ? [] : [1]\n    content {}\n  }\n}\n", false},
		"template wrap": {"locals {\n  description = " + co("") + "\n}\n", "locals {\n  description = " + co(`, var.t_new ? "${var.desc}" : null`) + "\n}\n", false},
		"expanded last argument": {"locals {\n  description = try(coalesce(var.t00 ? \"x\" : null, \"a\", local.l...), null)\n}\n",
			"locals {\n  description = try(coalesce(var.t00 ? \"x\" : null, \"a\", var.t_new ? \"n\" : null, local.l...), null)\n}\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			r := runPair(t, map[string]string{"variables.tf": vars, "main.tf": c.base}, map[string]string{"variables.tf": vars + newVar, "main.tf": c.head}, "")
			var trs []Trigger
			for _, tr := range r.Triggers {
				if tr.Line != nil && tr.File == "main.tf" {
					trs = append(trs, tr)
				}
			}
			if len(trs) == 0 {
				t.Fatalf("no trigger: %s", mustJSON(t, r))
			}
			for _, tr := range trs {
				if proven(tr) != c.proof {
					t.Errorf("%s %v: proof %v, want %v: %s", tr.Category, tr.Levels, proven(tr), c.proof, str(tr.Reason))
				}
				for _, cs := range tr.Cases {
					if in, ok := cs.Inputs["t_new"]; c.proof && (!ok || string(in.Value) != "false") {
						t.Errorf("%s case %s: t_new %s", tr.ID, cs.ID, mustJSON(t, in))
					}
				}
			}
			if name == "resource argument" && (len(trs) != 1 || len(trs[0].AlsoCategories) == 0) {
				t.Errorf("want one merged trigger: %s", mustJSON(t, trs))
			}
		})
	}
}
