package behaviour

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// The fixtures are synthetic modules shaped like a generated module
// repository: a root module, its variables and a generated wrapper.

const asgVars = `variable "name" {
  type    = string
  default = null
}
`

const asgMain = `resource "aws_autoscaling_group" "this" {
  name = var.name
}
`

const asgWrapperVars = `variable "defaults" {
  type    = any
  default = {}
}

variable "items" {
  type    = any
  default = {}
}
`

const asgWrapper = `module "wrapper" {
  source = "../"

  for_each = var.items

  name = try(each.value.name, var.defaults.name, null)
}
`

// asgNewVar is the variable the change adds.
const asgNewVar = `
variable "alarm_specification" {
  description = "The alarm specification"
  type        = object({ alarms = optional(list(string)) })
  default     = null
}
`

// asgHeadMain reads the new variable in an added dynamic block only.
const asgHeadMain = `resource "aws_autoscaling_group" "this" {
  name = var.name

  dynamic "alarm_specification" {
    for_each = var.alarm_specification != null ? [var.alarm_specification] : []
    content {
      alarms = alarm_specification.value.alarms
    }
  }
}
`

const asgLine = "  alarm_specification = try(each.value.alarm_specification, var.defaults.alarm_specification, null)\n"

func asgBase() map[string]string {
	return map[string]string{"main.tf": asgMain, "variables.tf": asgVars,
		"wrappers/main.tf": asgWrapper, "wrappers/variables.tf": asgWrapperVars}
}

// asgHead is the head of the replay, with the wrapper line given.
func asgHead(line string) map[string]string {
	h := asgBase()
	h["main.tf"] = asgHeadMain
	h["variables.tf"] = asgVars + asgNewVar
	h["wrappers/main.tf"] = strings.Replace(asgWrapper, "\n}\n", "\n"+line+"}\n", 1)
	return h
}

func with(files map[string]string, kv ...string) map[string]string {
	out := maps.Clone(files)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			delete(out, kv[i])
			continue
		}
		out[kv[i]] = kv[i+1]
	}
	return out
}

// find returns the trigger of a file at an address and attribute ("" for
// none), failing when there is not exactly one.
func find(t *testing.T, r *Report, file, addr, attr string) Trigger {
	t.Helper()
	var hits []Trigger
	for _, tr := range r.Triggers {
		if tr.File == file && deref(tr.Address) == addr && deref(tr.Attribute) == attr {
			hits = append(hits, tr)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("%d triggers at %s %s %s:\n%s", len(hits), file, addr, attr, mustJSON(t, r))
	}
	return hits[0]
}

func wantFollower(t *testing.T, tr Trigger, follows ...string) {
	t.Helper()
	if tr.Follows == nil || len(tr.Levels) != 0 || tr.Reason == nil || !slices.Equal(tr.Follows, append([]string{}, follows...)) {
		t.Errorf("%s: follows %v, levels %v, want follows %v and no level", tr.ID, tr.Follows, tr.Levels, follows)
	}
}

func wantNotFollower(t *testing.T, tr Trigger) {
	t.Helper()
	if tr.Follows != nil || !slices.Equal(tr.Levels, []string{LevelL4}) {
		t.Errorf("%s: follows %v, levels %v, want no follows and L4", tr.ID, tr.Follows, tr.Levels)
	}
}

// asgCoverage plans the root module's autoscaling group with the dynamic
// block's blocks.
const asgCoverage = `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,
  "addresses":["module.asg.aws_autoscaling_group.this"],
  "attributes":{"module.asg.aws_autoscaling_group.this":["alarm_specification","name"]},"module_calls":{"asg":"."}}]}`

// TestFollowsAutoscalingReplay: an added optional variable read only in an
// added dynamic block, and its generated wrapper line. The variable follows
// the dynamic block's trigger and the line follows the variable, so both
// are covered when the block is: the review ends conclusive.
func TestFollowsAutoscalingReplay(t *testing.T) {
	r := runPair(t, asgBase(), asgHead(asgLine), "")
	dyn := find(t, r, "main.tf", "aws_autoscaling_group.this", "dynamic.alarm_specification")
	v := find(t, r, "variables.tf", "var.alarm_specification", "")
	line := find(t, r, "wrappers/main.tf", "module.wrapper", "alarm_specification")
	wantFollower(t, v, dyn.ID)
	wantFollower(t, line, v.ID)
	if len(r.Triggers) != 3 {
		t.Errorf("want 3 triggers:\n%s", mustJSON(t, r))
	}
	got := mustJSON(t, r)
	example := "../../schema/examples/behaviour.valid.follows.json"
	if *update {
		if err := os.WriteFile(example, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if e, err := os.ReadFile(example); err != nil || !bytes.Equal(e, got) {
		t.Errorf("%s differs from the report (go test -update rewrites it):\n%s", example, got)
	}
	// The report reads back, and claims-eval follows the covered block.
	b, err := ReadBehaviour(writeJSON(t, "behaviour.json", string(got)))
	if err != nil {
		t.Fatal(err)
	}
	tr, _ := states(evalWith(t, b, ladder(t, "", asgCoverage)))
	for _, id := range []string{dyn.ID, v.ID, line.ID} {
		if tr[id] != StateCovered {
			t.Errorf("%s: %s, want covered", id, tr[id])
		}
	}
	// With no plan the block, and so both followers, stay unverifiable.
	tr, _ = states(evalWith(t, b, ladder(t, "", noCoverage)))
	for _, id := range []string{dyn.ID, v.ID, line.ID} {
		if tr[id] != StateUnverifiable {
			t.Errorf("no coverage: %s: %s, want unverifiable", id, tr[id])
		}
	}
}

// TestFollowsCloudwatchReplay: the added variable is read by an argument
// added to an existing resource, which is both an L4 change and, with no
// schema, an unknown propagation candidate. The plan covers the first; the
// second has no run, so the variable and its wrapper line stay unverifiable
// through it.
func TestFollowsCloudwatchReplay(t *testing.T) {
	group := `resource "aws_cloudwatch_log_group" "this" {
  name = var.name
}
`
	vars := `variable "name" {
  type    = string
  default = null
}
`
	wrapper := `module "wrapper" {
  source = "../../modules/log-group"

  for_each = var.items

  name = try(each.value.name, var.defaults.name, null)
}
`
	base := map[string]string{"modules/log-group/main.tf": group, "modules/log-group/variables.tf": vars,
		"wrappers/log-group/main.tf": wrapper, "wrappers/log-group/variables.tf": asgWrapperVars}
	head := with(base,
		"modules/log-group/main.tf", strings.Replace(group, "\n}", "\n  deletion_protection_enabled = var.deletion_protection_enabled\n}", 1),
		"modules/log-group/variables.tf", vars+"\nvariable \"deletion_protection_enabled\" {\n  type    = bool\n  default = null\n}\n",
		"wrappers/log-group/main.tf", strings.Replace(wrapper, "\n}\n",
			"\n  deletion_protection_enabled = try(each.value.deletion_protection_enabled, var.defaults.deletion_protection_enabled, null)\n}\n", 1))
	r := runPair(t, base, head, "")
	var uses []string
	for _, tr := range r.Triggers {
		if tr.File == "modules/log-group/main.tf" {
			uses = append(uses, tr.ID)
		}
	}
	if len(uses) != 2 {
		t.Fatalf("want the L4 trigger and the candidate at the use site:\n%s", mustJSON(t, r))
	}
	v := find(t, r, "modules/log-group/variables.tf", "var.deletion_protection_enabled", "")
	line := find(t, r, "wrappers/log-group/main.tf", "module.wrapper", "deletion_protection_enabled")
	wantFollower(t, v, uses...)
	wantFollower(t, line, v.ID)
	cov := `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,
	  "addresses":["module.lg.aws_cloudwatch_log_group.this"],
	  "attributes":{"module.lg.aws_cloudwatch_log_group.this":["deletion_protection_enabled"]},"module_calls":{"lg":"modules/log-group"}}]}`
	ev := evalWith(t, r, ladder(t, "", cov))
	tr, _ := states(ev)
	want := map[string]string{uses[0]: StateCovered, uses[1]: StateUnverifiable, v.ID: StateUnverifiable, line.ID: StateUnverifiable}
	for id, s := range want {
		if tr[id] != s {
			t.Errorf("%s: %s, want %s\n%s", id, tr[id], s, mustJSON(t, ev))
		}
	}
	for _, s := range ev.Triggers {
		if s.ID == v.ID && !strings.Contains(s.Reason, uses[1]) {
			t.Errorf("reason %q does not name %s", s.Reason, uses[1])
		}
	}
}

// TestRuleAAccepts: the variable follows the triggers that hold its reads,
// wherever they are, and nothing when no expression of its module reads it.
func TestRuleAAccepts(t *testing.T) {
	unused := with(asgHead(asgLine), "main.tf", asgMain)
	cases := map[string]struct {
		head    map[string]string
		follows func(r *Report) []string
	}{
		"unused": {head: unused, follows: func(*Report) []string { return nil }},
		"var.x in a plain string is no read": {
			head:    with(unused, "main.tf", strings.Replace(asgMain, "\n}", "\n  description = \"var.alarm_specification\"\n}", 1)),
			follows: func(*Report) []string { return nil },
		},
		"a read in an added resource": {
			head: with(unused, "extra.tf", "resource \"aws_thing\" \"t\" {\n  spec = var.alarm_specification\n}\n"),
			follows: func(r *Report) []string {
				return []string{find(t, r, "extra.tf", "aws_thing.t", "").ID}
			},
		},
		"a read only via an added local": {
			head: with(unused, "locals.tf", "locals {\n  alarms = var.alarm_specification\n}\n"),
			follows: func(r *Report) []string {
				return []string{find(t, r, "locals.tf", "local.alarms", "").ID}
			},
		},
		"a read inside dynamic content": {
			head: with(asgHead(asgLine),
				"main.tf", strings.Replace(asgHeadMain, "alarm_specification.value.alarms", "coalesce(alarm_specification.value.alarms, var.alarm_specification.alarms)", 1)),
			follows: func(r *Report) []string {
				return []string{find(t, r, "main.tf", "aws_autoscaling_group.this", "dynamic.alarm_specification").ID}
			},
		},
		"var[\"x\"] is a read": {
			head: with(unused, "extra.tf", "resource \"aws_thing\" \"t\" {\n  spec = var[\"alarm_specification\"]\n}\n"),
			follows: func(r *Report) []string {
				return []string{find(t, r, "extra.tf", "aws_thing.t", "").ID}
			},
		},
		"{} against an object of optional attributes": {
			head:    with(unused, "variables.tf", asgVars+strings.Replace(asgNewVar, "default     = null", "default     = {}", 1)),
			follows: func(*Report) []string { return nil },
		},
		"a number default": {
			head:    with(unused, "variables.tf", asgVars+"variable \"alarm_specification\" {\n  type    = number\n  default = 30\n}\n"),
			follows: func(*Report) []string { return nil },
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := runPair(t, asgBase(), tc.head, "")
			v := find(t, r, "variables.tf", "var.alarm_specification", "")
			wantFollower(t, v, tc.follows(r)...)
		})
	}
	// Nothing reads it: verified, under the walk the classifier made.
	r := runPair(t, asgBase(), unused, "")
	v := find(t, r, "variables.tf", "var.alarm_specification", "")
	if deref(v.Reason) != reasonFollowsNone {
		t.Errorf("reason %q", deref(v.Reason))
	}
	if tr, _ := states(evalWith(t, r, ladder(t, "", noCoverage))); tr[v.ID] != StateVerified {
		t.Errorf("unused: %s", tr[v.ID])
	}
}

// TestRuleASameNameElsewhere: a read of a variable of the same name in
// another module directory is no read of this one.
func TestRuleASameNameElsewhere(t *testing.T) {
	other := "variable \"alarm_specification\" {\n  default = null\n}\n\nresource \"aws_thing\" \"o\" {\n  spec = var.alarm_specification\n}\n"
	base := with(asgBase(), "modules/other/main.tf", other)
	head := with(asgHead(asgLine), "main.tf", asgMain, "modules/other/main.tf", "# a comment\n"+other)
	r := runPair(t, base, head, "")
	wantFollower(t, find(t, r, "variables.tf", "var.alarm_specification", ""))
	// The same in the wrapper directory: var.defaults.N reads defaults.
	head = with(asgHead(asgLine), "main.tf", asgMain,
		"wrappers/variables.tf", asgWrapperVars+"\nvariable \"alarm_specification\" {\n  default = null\n}\n")
	r = runPair(t, asgBase(), head, "")
	wantFollower(t, find(t, r, "wrappers/variables.tf", "var.alarm_specification", ""))
}

// TestRuleADeclines: each gate, failed, leaves the variable at L4 and its
// wrapper line with it.
func TestRuleADeclines(t *testing.T) {
	newVar := func(body string) map[string]string {
		return with(asgHead(asgLine), "variables.tf", asgVars+"\nvariable \"alarm_specification\" {\n"+body+"}\n")
	}
	added := func(body string) map[string]string {
		return with(asgHead(asgLine), "main.tf", asgMain, "extra.tf", body)
	}
	cases := map[string]map[string]string{
		"no default":                        newVar("  type = any\n"),
		"validation":                        newVar("  default = null\n  validation {\n    condition     = true\n    error_message = \"x\"\n  }\n"),
		"sensitive":                         newVar("  default   = null\n  sensitive = true\n"),
		"ephemeral":                         newVar("  default   = null\n  ephemeral = true\n"),
		"nullable = false":                  newVar("  default  = \"a\"\n  nullable = false\n"),
		"a default the type does not admit": newVar("  type    = number\n  default = \"abc\"\n"),
		"{} against required attributes":    newVar("  type    = object({ alarms = list(string) })\n  default = {}\n"),
		"a heredoc default":                 newVar("  default = <<EOT\nx\nEOT\n"),
		"a non-empty tuple default":         newVar("  default = [\"a\"]\n"),
		"a function default":                newVar("  default = upper(\"a\")\n"),
		"an interpolated default":           newVar("  default = \"${\"a\"}\"\n"),
		"a .tf.json file":                   with(asgHead(asgLine), "extra.tf.json", "{}\n"),
		"a templatefile read": added("resource \"aws_thing\" \"t\" {\n" +
			"  spec = templatefile(\"t.tpl\", { a = var.alarm_specification })\n}\n"),
		"no read but a file call": added("resource \"aws_thing\" \"t\" {\n  spec = file(\"x\")\n}\n"),
		"a read no trigger holds": with(asgHead(asgLine), "main.tf", asgMain,
			"provisioned.tf", "resource \"aws_thing\" \"p\" {\n  provisioner \"local-exec\" {\n    command = var.alarm_specification\n  }\n}\n"),
		"a reference the walk cannot name": added("resource \"aws_thing\" \"t\" {\n  spec = var[local.k]\n}\n"),
		"another argument":                 newVar("  default    = null\n  deprecated = \"x\"\n"),
		"a non-empty object default":       newVar("  default = { a = 1 }\n"),
		"a read in a nested block of a new type": with(asgHead(asgLine), "main.tf", asgMain,
			"provisioned.tf", "resource \"aws_thing\" \"p\" {\n  timeouts {\n    create = var.alarm_specification\n  }\n}\n"),
		// One file, one resource: a read before and a read after the added
		// argument lie outside its source.
		"a read before the argument": with(asgHead(asgLine), "main.tf", asgMain,
			"provisioned.tf", "resource \"aws_thing\" \"p\" {\n  provisioner \"local-exec\" {\n    command = var.alarm_specification\n  }\n  spec = var.alarm_specification\n}\n"),
		"a read after the argument": with(asgHead(asgLine), "main.tf", asgMain,
			"provisioned.tf", "resource \"aws_thing\" \"p\" {\n  spec = var.alarm_specification\n  provisioner \"local-exec\" {\n    command = var.alarm_specification\n  }\n}\n"),
	}
	for name, head := range cases {
		t.Run(name, func(t *testing.T) {
			base := asgBase()
			if strings.HasPrefix(name, "a read ") {
				// The resource is there at the base; the head adds to it.
				base = with(base, "provisioned.tf", "resource \"aws_thing\" \"p\" {\n}\n")
			}
			r := runPair(t, base, head, "")
			v := find(t, r, "variables.tf", "var.alarm_specification", "")
			wantNotFollower(t, v)
			wantNotFollower(t, find(t, r, "wrappers/main.tf", "module.wrapper", "alarm_specification"))
		})
	}
	// A read the base already had: no trigger holds it.
	base := with(asgBase(), "extra.tf", "resource \"aws_thing\" \"t\" {\n  spec = var.alarm_specification\n}\n")
	head := with(asgHead(asgLine), "main.tf", asgMain, "extra.tf", base["extra.tf"])
	wantNotFollower(t, find(t, runPair(t, base, head, ""), "variables.tf", "var.alarm_specification", ""))
	// A file that does not parse: the directory is not decomposed.
	r := runPair(t, asgBase(), with(asgHead(asgLine), "broken.tf", "resource {\n"), "")
	for _, tr := range r.Triggers {
		if tr.Follows != nil && path.Dir(tr.File) == "." {
			t.Errorf("%s follows %v in an undecomposed directory", tr.ID, tr.Follows)
		}
	}
}

// TestRuleAExistingVariable: a changed default stays a default change, and
// a variable moved between files is no change.
func TestRuleAExistingVariable(t *testing.T) {
	head := with(asgBase(), "variables.tf", strings.Replace(asgVars, "default = null", "default = \"x\"", 1))
	r := runPair(t, asgBase(), head, "")
	if tr := find(t, r, "variables.tf", "var.name", "default"); tr.Category != CatDefault || tr.Follows != nil {
		t.Errorf("changed default: %s %v", tr.Category, tr.Follows)
	}
	// A removed optional variable nothing reads follows nothing.
	base := with(asgBase(), "variables.tf", asgVars+asgNewVar)
	if tr := find(t, runPair(t, base, asgBase(), ""), "variables.tf", "var.alarm_specification", ""); tr.ChangeKind != KindRemoved {
		t.Errorf("removed variable: %s", tr.ChangeKind)
	} else {
		wantNotFollower(t, tr)
	}
	head = with(asgBase(), "variables.tf", "", "inputs.tf", asgVars)
	if r := runPair(t, asgBase(), head, ""); len(r.Triggers) != 0 || !r.Inert {
		t.Errorf("moved variable:\n%s", mustJSON(t, r))
	}
}

// TestRuleBDeclines: every wrapper line but the generated form, and every
// wrapper the rule does not name, stays L4 while the variable follows.
func TestRuleBDeclines(t *testing.T) {
	n := "alarm_specification"
	lines := map[string]string{
		"no default form":   "  " + n + " = try(each.value." + n + ", var.defaults." + n + ")\n",
		"{} fallback":       "  " + n + " = try(each.value." + n + ", var.defaults." + n + ", {})\n",
		"swapped order":     "  " + n + " = try(var.defaults." + n + ", each.value." + n + ", null)\n",
		"lookup":            "  " + n + " = lookup(each.value, \"" + n + "\", null)\n",
		"an extra argument": "  " + n + " = try(each.value." + n + ", var.defaults." + n + ", null, null)\n",
		"each.key":          "  " + n + " = try(each.key." + n + ", var.defaults." + n + ", null)\n",
		"each.value index":  "  " + n + " = try(each.value[\"" + n + "\"], var.defaults." + n + ", null)\n",
		"heredoc fallback":  "  " + n + " = try(each.value." + n + ", var.defaults." + n + ", <<EOT\nnull\nEOT\n  )\n",
		"a spliced string":  "  " + n + " = try(each.value." + n + ", var.defaults." + n + ", \"null\")\n",
	}
	for name, line := range lines {
		t.Run(name, func(t *testing.T) {
			r := runPair(t, asgBase(), asgHead(line), "")
			wantFollower(t, find(t, r, "variables.tf", "var."+n, ""), find(t, r, "main.tf", "aws_autoscaling_group.this", "dynamic."+n).ID)
			wantNotFollower(t, find(t, r, "wrappers/main.tf", "module.wrapper", n))
		})
	}
	other := map[string]struct {
		base, head map[string]string
		file, addr string
	}{
		"a changed source": {asgBase(), with(asgHead(asgLine), "wrappers/main.tf",
			strings.Replace(asgHead(asgLine)["wrappers/main.tf"], `"../"`, `"../."`, 1)), "wrappers/main.tf", "module.wrapper"},
		"for_each not var.items": {with(asgBase(), "wrappers/main.tf", strings.Replace(asgWrapper, "var.items", "var.defaults", 1)),
			with(asgHead(asgLine), "wrappers/main.tf", strings.Replace(asgHead(asgLine)["wrappers/main.tf"], "var.items", "var.defaults", 1)),
			"wrappers/main.tf", "module.wrapper"},
		"two levels deep": {with(asgBase(), "wrappers/main.tf", "", "wrappers/a/b/main.tf", strings.Replace(asgWrapper, `"../"`, `"../../../"`, 1)),
			with(asgHead(asgLine), "wrappers/main.tf", "", "wrappers/a/b/main.tf",
				strings.Replace(asgHead(asgLine)["wrappers/main.tf"], `"../"`, `"../../../"`, 1)), "wrappers/a/b/main.tf", "module.wrapper"},
		"a non-wrapper child passthrough": {with(asgBase(), "wrappers/main.tf", "", "callers/main.tf", asgWrapper),
			with(asgHead(asgLine), "wrappers/main.tf", "", "callers/main.tf", asgHead(asgLine)["wrappers/main.tf"]),
			"callers/main.tf", "module.wrapper"},
		"a module not named wrapper": {with(asgBase(), "wrappers/main.tf", strings.Replace(asgWrapper, `"wrapper"`, `"other"`, 1)),
			with(asgHead(asgLine), "wrappers/main.tf", strings.Replace(asgHead(asgLine)["wrappers/main.tf"], `"wrapper"`, `"other"`, 1)),
			"wrappers/main.tf", "module.other"},
	}
	other["a file other than main.tf"] = struct {
		base, head map[string]string
		file, addr string
	}{with(asgBase(), "wrappers/main.tf", "", "wrappers/other.tf", asgWrapper),
		with(asgHead(asgLine), "wrappers/main.tf", "", "wrappers/other.tf", asgHead(asgLine)["wrappers/main.tf"]),
		"wrappers/other.tf", "module.wrapper"}
	for name, tc := range other {
		t.Run(name, func(t *testing.T) {
			r := runPair(t, tc.base, tc.head, "")
			wantNotFollower(t, find(t, r, tc.file, tc.addr, n))
		})
	}
	// A line the base already had in another form, rewritten to the
	// generator form, is a change, not an added line.
	changed := with(asgBase(), "wrappers/main.tf",
		asgHead("  " + n + " = lookup(each.value, \"" + n + "\", null)\n")["wrappers/main.tf"])
	tr := find(t, runPair(t, changed, asgHead(asgLine), ""), "wrappers/main.tf", "module.wrapper", n)
	if tr.ChangeKind != KindModified || tr.Follows != nil || len(tr.Levels) == 0 {
		t.Errorf("changed line: %s follows %v levels %v", tr.ChangeKind, tr.Follows, tr.Levels)
	}
	// The module default is a string: a null fallback is not its default,
	// and the line with the string accepted is.
	str := with(asgHead(asgLine), "variables.tf", asgVars+"\nvariable \""+n+"\" {\n  type    = string\n  default = \"a\"\n}\n")
	wantNotFollower(t, find(t, runPair(t, asgBase(), str, ""), "wrappers/main.tf", "module.wrapper", n))
	str["wrappers/main.tf"] = asgHead("  " + n + " = try(each.value." + n + ", var.defaults." + n + ", \"a\")\n")["wrappers/main.tf"]
	r := runPair(t, asgBase(), str, "")
	wantFollower(t, find(t, r, "wrappers/main.tf", "module.wrapper", n), find(t, r, "variables.tf", "var."+n, "").ID)
	// A line written across lines and with comments is the same tokens.
	spread := "  " + n + " = try( # passthrough\n    each.value." + n + ",\n    var.defaults." + n + ",\n    null\n  )\n"
	r = runPair(t, asgBase(), asgHead(spread), "")
	wantFollower(t, find(t, r, "wrappers/main.tf", "module.wrapper", n), find(t, r, "variables.tf", "var."+n, "").ID)
}

// TestFollowsPastTheCap: a variable read by more triggers than the report
// lists cannot follow them all, so it selects its category's levels.
func TestFollowsPastTheCap(t *testing.T) {
	var locals strings.Builder
	locals.WriteString("locals {\n")
	for i := range MaxTriggers + 5 {
		fmt.Fprintf(&locals, "  l%02d = var.alarm_specification\n", i)
	}
	locals.WriteString("}\n")
	// zz.tf sorts after the wrapper, so the reads are the triggers past
	// the cap and the wrapper line is listed: it follows a reverted
	// variable, so it reverts too.
	head := with(asgHead(asgLine), "main.tf", asgMain, "variables.tf", asgVars, "a.tf", asgNewVar, "zz.tf", locals.String())
	r := runPair(t, asgBase(), head, "")
	if r.Capped == 0 {
		t.Fatalf("not capped:\n%s", mustJSON(t, r))
	}
	for _, tr := range []Trigger{find(t, r, "a.tf", "var.alarm_specification", ""),
		find(t, r, "wrappers/main.tf", "module.wrapper", "alarm_specification")} {
		if tr.Follows != nil || !slices.Equal(tr.Levels, []string{LevelL4}) || deref(tr.Reason) != reasonFollowCapped {
			t.Errorf("%s capped follows: %v %v %q", tr.ID, tr.Follows, tr.Levels, deref(tr.Reason))
		}
	}
}

// TestFollowsMergedTrigger: a read in a changed data source argument lies
// in two specs that merge into one trigger; the variable follows the
// merged trigger's id.
func TestFollowsMergedTrigger(t *testing.T) {
	data := "data \"aws_thing\" \"d\" {\n  name = \"x\"\n}\n"
	base := with(asgBase(), "data.tf", data)
	head := with(asgHead(asgLine), "main.tf", asgMain,
		"data.tf", strings.Replace(data, `"x"`, `coalesce(var.alarm_specification, "x")`, 1))
	r := runPair(t, base, head, "")
	d := find(t, r, "data.tf", "data.aws_thing.d", "name")
	if len(d.AlsoCategories) == 0 {
		t.Fatalf("not merged:\n%s", mustJSON(t, r))
	}
	wantFollower(t, find(t, r, "variables.tf", "var.alarm_specification", ""), d.ID)
}

// TestFollowsDeduplicated: a follower of a spec that the report drops as a
// duplicate of another follows the trigger that stays.
func TestFollowsDeduplicated(t *testing.T) {
	c := &classifier{views: map[string][2]*view{}}
	dup := spec{file: "main.tf", line: 2, cat: CatResourceAddRemove, kind: KindAdded, addr: "aws_thing.t"}
	a, b := dup, dup
	a.sid, b.sid = 0, 1
	f := spec{file: "variables.tf", line: 1, cat: CatUnclassified, kind: KindAdded, addr: "var.x",
		levels: []string{}, reason: reasonFollowsReads, sid: 2, follows: []int{1}}
	r := c.report(nil, []spec{a, b, f}, nil)
	if len(r.Triggers) != 2 {
		t.Fatalf("not deduplicated:\n%s", mustJSON(t, r))
	}
	wantFollower(t, find(t, r, "variables.tf", "var.x", ""), find(t, r, "main.tf", "aws_thing.t", "").ID)
}

// TestEvaluateFollowers: claims-eval decides a follower from the triggers
// it follows, and fails closed on a dangling id, a cycle, a refuted use
// site or a claim of the follower.
func TestEvaluateFollowers(t *testing.T) {
	line := 3
	l4 := func(id, attr string) Trigger {
		return Trigger{ID: id, File: "main.tf", Line: &line, Category: CatDynamic, Address: new("aws_s3_bucket.b"),
			Attribute: new(attr), Levels: []string{LevelL4}}
	}
	follower := func(id string, follows ...string) Trigger {
		return Trigger{ID: id, File: "variables.tf", Line: &line, Category: CatUnclassified, Address: new("var." + id),
			Levels: []string{}, Reason: new(reasonFollowsReads), Follows: append([]string{}, follows...)}
	}
	b := &Report{SchemaVersion: 1, Entries: []Entry{}, Triggers: []Trigger{
		l4("T-001", "dynamic.rule"), l4("T-002", "tags"),
		follower("T-003", "T-001"),                             // covered
		follower("T-004", "T-001", "T-002"),                    // T-002 is not planned
		follower("T-005", "T-099"),                             // dangling
		follower("T-006", "T-007"), follower("T-007", "T-006"), // a cycle
		follower("T-008", "T-008"), // itself
		follower("T-009", "T-003"), // a chain
		follower("T-010"),          // nothing reads it
		follower("T-011", "T-001"), // a claim of it
		follower("T-012", "T-013"), // a refuted use site
		l4("T-013", "dynamic.rule"),
		follower("T-014", "T-010"), // follows a verified follower only
		follower("T-015", "T-001"), // in an inert entry
	}}
	b.Entries = []Entry{{Path: "inert.tf", ChangeKind: KindModified, Inert: true, TriggerIDs: []string{"T-015"}}}
	cov := `{"class":"complete","examples":[{"dir":"examples/complete","head":"planned","base":null,
	  "addresses":["aws_s3_bucket.b"],"attributes":{"aws_s3_bucket.b":["rule"]}}]}`
	b.Triggers[0].File, b.Triggers[12].File = "examples/complete/main.tf", "examples/complete/main.tf"
	runs := `{"trigger_id":"T-013","level":"L4","state":"ran","reason":null,"fixture_sha256":null,"control":"valid",
	  "observations":{"base":[],"head":[{"case":"plan","known":true,"action":"create"}]}}`
	r := evalWith(t, b, ladder(t, runs, cov),
		claim("C-1", PredPlanAction, `{"node":"T-011","revision":"head","action":"create"}`),
		claim("C-2", PredPlanAction, `{"node":"T-013","revision":"head","action":"delete"}`))
	tr, _ := states(r)
	want := map[string]string{"T-001": StateCovered, "T-002": StateUnverifiable, "T-003": StateCovered,
		"T-004": StateUnverifiable, "T-005": StateUnverifiable, "T-006": StateUnverifiable, "T-007": StateUnverifiable,
		"T-008": StateUnverifiable, "T-009": StateCovered, "T-010": StateVerified, "T-011": StateUnverifiable,
		"T-012": StateUnverifiable, "T-013": StateRefuted, "T-014": StateVerified, "T-015": StateInert}
	for id, s := range want {
		if tr[id] != s {
			t.Errorf("%s: %s, want %s\n%s", id, tr[id], s, mustJSON(t, r))
		}
	}
	// A refuted claim of the follower refutes it.
	r = evalWith(t, b, ladder(t, `{"trigger_id":"T-011","level":"L4","state":"ran","reason":null,"fixture_sha256":null,"control":"valid",
	  "observations":{"base":[],"head":[{"case":"plan","known":true,"action":"create"}]}}`, cov),
		claim("C-1", PredPlanAction, `{"node":"T-011","revision":"head","action":"delete"}`))
	if tr, _ := states(r); tr["T-011"] != StateRefuted {
		t.Errorf("refuted claim of a follower: %s", tr["T-011"])
	}
}

// TestReadBehaviourFollows: a follower selects no level, and follows
// trigger ids.
func TestReadBehaviourFollows(t *testing.T) {
	doc := func(levels, follows string) string {
		return `{"schema_version":1,"inert":false,"entries":[],"capped":0,"triggers":[{"id":"T-001","file":"v.tf","line":1,
		  "category":"unclassified-change","change_kind":"added","address":"var.x","attribute":null,"base_expr":null,"head_expr":null,
		  "referenced_inputs":[],"contextual_refs":[],"contextual_ref_details":[],"pure":false,"levels":` + levels + `,"reason":"r","follows":` + follows + `}]}`
	}
	if _, err := ReadBehaviour(writeJSON(t, "b.json", doc(`[]`, `["T-002"]`))); err != nil {
		t.Errorf("follower: %v", err)
	}
	for name, d := range map[string]string{"levels": doc(`["L4"]`, `["T-002"]`), "id": doc(`[]`, `["x"]`)} {
		if _, err := ReadBehaviour(writeJSON(t, "b.json", d)); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}
