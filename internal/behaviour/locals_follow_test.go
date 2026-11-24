package behaviour

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The fixtures of an added local: a module with one resource, and a head
// that adds a local and reads it.

const lfVars = `variable "name" {
  type    = string
  default = null
}
`

const lfMain = `resource "aws_thing" "t" {
  name = var.name
}
`

const lfLocal = "locals {\n  full = \"${var.name}-x\"\n}\n"

// lfReader is an added resource that reads the added local.
const lfReader = "resource \"aws_widget\" \"w\" {\n  name = local.full\n}\n"

func lfBase() map[string]string {
	return map[string]string{"main.tf": lfMain, "variables.tf": lfVars}
}

// lfHead adds the local and the files given.
func lfHead(kv ...string) map[string]string {
	return with(lfBase(), append([]string{"locals.tf", lfLocal}, kv...)...)
}

// wantLocalNotFollower holds an added or removed local to its category's
// level, L1, with no follows.
func wantLocalNotFollower(t *testing.T, tr Trigger) {
	t.Helper()
	if tr.Follows != nil || !slices.Equal(tr.Levels, []string{LevelL1}) {
		t.Errorf("%s: follows %v, levels %v, want no follows and L1", tr.ID, tr.Follows, tr.Levels)
	}
}

// TestAddedLocalFollowsReader: an added local read only by an added
// resource follows that resource's trigger, selects no level and gets no
// cases, so it is covered when the resource is planned and unverifiable
// when it is not.
func TestAddedLocalFollowsReader(t *testing.T) {
	r := runPair(t, lfBase(), lfHead("extra.tf", lfReader), "")
	res := find(t, r, "extra.tf", "aws_widget.w", "")
	loc := find(t, r, "locals.tf", "local.full", "")
	wantFollower(t, loc, res.ID)
	if deref(loc.Reason) != reasonFollowsLocal || loc.Cases != nil {
		t.Errorf("reason %q, cases %v", deref(loc.Reason), loc.Cases)
	}
	cov := `{"class":"complete","examples":[{"dir":".","head":"planned","base":null,
	  "addresses":["aws_widget.w"],"attributes":{"aws_widget.w":["name"]}}]}`
	tr, _ := states(evalWith(t, r, ladder(t, "", cov)))
	if tr[res.ID] != StateCovered || tr[loc.ID] != StateCovered {
		t.Errorf("planned: resource %s, local %s, want covered", tr[res.ID], tr[loc.ID])
	}
	tr, _ = states(evalWith(t, r, ladder(t, "", noCoverage)))
	if tr[loc.ID] != StateUnverifiable {
		t.Errorf("no plan: local %s, want unverifiable", tr[loc.ID])
	}
}

// TestAddedLocalToggleReplay replays an added local that is anytrue of 16
// toggles, read by a changed local that now reads it: the added local
// follows the changed one, so it no longer asks for a sweep over the cap;
// the changed local keeps its own reason, since no case stands in for a
// local reference.
func TestAddedLocalToggleReplay(t *testing.T) {
	vars := "variable \"create\" {\n  type    = bool\n  default = true\n}\n\nvariable \"docs\" {\n  type    = list(string)\n  default = []\n}\n"
	var reads []string
	for i := 1; i <= 16; i++ {
		vars += fmt.Sprintf("\nvariable \"t%02d\" {\n  type    = bool\n  default = false\n}\n", i)
		reads = append(reads, fmt.Sprintf("    var.t%02d,\n", i))
	}
	base := map[string]string{"variables.tf": vars,
		"main.tf": "locals {\n  create = var.create && length(var.docs) > 0\n}\n\nresource \"aws_thing\" \"t\" {\n  count = local.create ? 1 : 0\n}\n"}
	head := with(base, "main.tf", "locals {\n  any = anytrue([\n"+strings.Join(reads, "")+"  ])\n"+
		"  create = var.create && (local.any || length(var.docs) > 0)\n}\n\nresource \"aws_thing\" \"t\" {\n  count = local.create ? 1 : 0\n}\n")
	r := runPair(t, base, head, "")
	create := find(t, r, "main.tf", "local.create", "")
	anyT := find(t, r, "main.tf", "local.any", "")
	wantFollower(t, anyT, create.ID)
	if anyT.Cases != nil {
		t.Errorf("the follower has cases: %v", anyT.Cases)
	}
	if !strings.Contains(deref(create.Reason), "local.any has no input states") {
		t.Errorf("changed local reason %q", deref(create.Reason))
	}
	tr, _ := states(evalWith(t, r, ladder(t, "", noCoverage)))
	if tr[anyT.ID] != StateUnverifiable || tr[create.ID] != StateUnverifiable {
		t.Errorf("states %v", tr)
	}
}

// TestAddedLocalAccepts: the reads the rule counts and the ones it does not.
func TestAddedLocalAccepts(t *testing.T) {
	// A variable of the same name, read where no trigger holds it, is no
	// read of the local.
	sameName := lfVars + "\nvariable \"full\" {\n  type    = string\n  default = null\n}\n"
	sameMain := "resource \"aws_thing\" \"t\" {\n  name = var.name\n  full = var.full\n}\n"
	r := runPair(t, with(lfBase(), "variables.tf", sameName, "main.tf", sameMain),
		lfHead("variables.tf", sameName, "main.tf", sameMain, "extra.tf", lfReader), "")
	wantFollower(t, find(t, r, "locals.tf", "local.full", ""), find(t, r, "extra.tf", "aws_widget.w", "").ID)
	// Two readers: an added resource and a changed argument.
	r = runPair(t, lfBase(), lfHead("extra.tf", lfReader, "main.tf", strings.Replace(lfMain, "var.name", "local.full", 1)), "")
	var want []string
	for _, tr := range r.Triggers {
		if tr.File == "main.tf" || tr.File == "extra.tf" {
			want = append(want, tr.ID)
		}
	}
	if len(want) < 2 {
		t.Fatalf("want triggers in main.tf and extra.tf:\n%s", mustJSON(t, r))
	}
	wantFollower(t, find(t, r, "locals.tf", "local.full", ""), want...)
}

// TestAddedLocalDeclines: each gate, failed, leaves the local at L1.
func TestAddedLocalDeclines(t *testing.T) {
	provisioned := "resource \"aws_thing\" \"p\" {\n  provisioner \"local-exec\" {\n    command = local.full\n  }\n}\n"
	cases := map[string]struct{ base, head map[string]string }{
		"nothing reads it": {lfBase(), lfHead()},
		"a read no trigger holds": {with(lfBase(), "provisioned.tf", "resource \"aws_thing\" \"p\" {\n}\n"),
			lfHead("provisioned.tf", provisioned)},
		"a templatefile read": {lfBase(), lfHead("extra.tf",
			"resource \"aws_widget\" \"w\" {\n  name = templatefile(\"t.tpl\", { a = local.full })\n}\n")},
		"a reference the walk cannot name": {lfBase(), lfHead("extra.tf",
			"resource \"aws_widget\" \"w\" {\n  name = local.full\n  spec = local[var.name]\n}\n")},
		"a .tf.json file at the head":  {lfBase(), lfHead("extra.tf", lfReader, "extra.tf.json", "{}\n")},
		"a .tf.json file at the base":  {with(lfBase(), "extra.tf.json", "{}\n"), lfHead("extra.tf", lfReader)},
		"a read in its own definition": {lfBase(), lfHead("locals.tf", "locals {\n  full = \"${local.full}-x\"\n}\n")},
		// An iterator named local: local.full may read the iterator, so the
		// real local's reads are not known.
		"a for iterator named local": {lfBase(), lfHead("extra.tf", lfReader,
			"other.tf", "output \"o\" {\n  value = [for local in [] : local.full]\n}\n")},
		"a for key named local": {lfBase(), lfHead("extra.tf", lfReader,
			"other.tf", "output \"o\" {\n  value = [for local, v in {} : v]\n}\n")},
		"a dynamic block labelled local": {lfBase(), lfHead("extra.tf",
			"resource \"aws_widget\" \"w\" {\n  name = local.full\n  dynamic \"local\" {\n    for_each = []\n    content {}\n  }\n}\n")},
		"a dynamic iterator named local": {lfBase(), lfHead("extra.tf",
			"resource \"aws_widget\" \"w\" {\n  name = local.full\n  dynamic \"x\" {\n    for_each = []\n    iterator = local\n    content {}\n  }\n}\n")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			wantLocalNotFollower(t, find(t, runPair(t, tc.base, tc.head, ""), "locals.tf", "local.full", ""))
		})
	}
	// A dynamic iterator with another name, labelled otherwise, keeps it.
	r := runPair(t, lfBase(), lfHead("extra.tf",
		"resource \"aws_widget\" \"w\" {\n  name = local.full\n  dynamic \"local\" {\n    for_each = []\n    iterator = it\n    content {}\n  }\n}\n"), "")
	wantFollower(t, find(t, r, "locals.tf", "local.full", ""), find(t, r, "extra.tf", "aws_widget.w", "").ID)
	// Another block labelled local is no iterator.
	r = runPair(t, lfBase(), lfHead("extra.tf", lfReader, "other.tf", "output \"local\" {\n  value = 1\n}\n"), "")
	wantFollower(t, find(t, r, "locals.tf", "local.full", ""), find(t, r, "extra.tf", "aws_widget.w", "").ID)
	// The same shadowing of var leaves an added variable at L4.
	head := with(asgHead(asgLine), "main.tf", asgMain,
		"extra.tf", "resource \"aws_thing\" \"t\" {\n  spec = var.alarm_specification\n  list = [for var in [] : var]\n}\n")
	wantNotFollower(t, find(t, runPair(t, asgBase(), head, ""), "variables.tf", "var.alarm_specification", ""))
	// A removed local the head still reads is no added local.
	r = runPair(t, with(lfBase(), "locals.tf", lfLocal), with(lfBase(), "extra.tf", lfReader), "")
	if tr := find(t, r, "locals.tf", "local.full", ""); tr.ChangeKind != KindRemoved {
		t.Errorf("removed local: %s", tr.ChangeKind)
	} else {
		wantLocalNotFollower(t, tr)
	}
	// A changed local stays a change of its own.
	r = runPair(t, with(lfBase(), "locals.tf", lfLocal), lfHead("locals.tf", strings.Replace(lfLocal, "-x", "-y", 1), "extra.tf", lfReader), "")
	if tr := find(t, r, "locals.tf", "local.full", ""); tr.ChangeKind != KindModified {
		t.Errorf("changed local: %s", tr.ChangeKind)
	} else {
		wantLocalNotFollower(t, tr)
	}
}
