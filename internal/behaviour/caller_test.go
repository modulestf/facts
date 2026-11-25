package behaviour

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/facts"
)

// callerTree is a module with a complete example and a test configuration,
// each calling the module with a literal argument.
func callerTree(name string) map[string]string {
	return map[string]string{
		"main.tf":                   "resource \"aws_s3_bucket\" \"b\" {\n  bucket = var.name\n}\n",
		"variables.tf":              "variable \"name\" {\n  type = string\n}\n",
		"examples/complete/main.tf": "module \"s3\" {\n  source = \"../../\"\n  name   = \"" + name + "\"\n}\n",
		"tests/basic/main.tf":       "module \"s3\" {\n  source = \"../../\"\n  name   = \"" + name + "\"\n}\n",
		"tests/basic.tftest.hcl":    "run \"plan\" {\n  command = plan\n}\n",
	}
}

// evalNoLadder evaluates r against a ladder that ran nothing.
func evalNoLadder(t *testing.T, r *Report) map[string]string {
	t.Helper()
	tr, _ := states(evalWith(t, r, ladder(t, "", `{"class":"none","examples":[]}`)))
	return tr
}

// byFile returns the state of each trigger by file, and whether every
// trigger of that file carries caller_code.
func byFile(t *testing.T, r *Report) (map[string]string, map[string]bool) {
	t.Helper()
	tr := evalNoLadder(t, r)
	state, marked := map[string]string{}, map[string]bool{}
	for _, tg := range r.Triggers {
		if s, ok := state[tg.File]; ok && s != tr[tg.ID] {
			state[tg.File] = "mixed"
		} else {
			state[tg.File] = tr[tg.ID]
		}
		if m, ok := marked[tg.File]; ok {
			marked[tg.File] = m && tg.CallerCode
		} else {
			marked[tg.File] = tg.CallerCode
		}
	}
	return state, marked
}

// TestCallerLiteralInert: a literal argument changed in an example or a
// test configuration is inert; the same change in the module is not.
func TestCallerLiteralInert(t *testing.T) {
	base := callerTree("a")
	head := with(callerTree("b"), "main.tf", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = var.name\n  acl    = \"public\"\n}\n")
	base["main.tf"] = "resource \"aws_s3_bucket\" \"b\" {\n  bucket = var.name\n  acl    = \"private\"\n}\n"
	r := runPair(t, base, head, "")
	state, marked := byFile(t, r)
	want := map[string]string{"examples/complete/main.tf": StateInert, "tests/basic/main.tf": StateInert, "main.tf": StateUnverifiable}
	for f, s := range want {
		if state[f] != s {
			t.Errorf("%s: %s, want %s\n%s", f, state[f], s, mustJSON(t, r))
		}
		if marked[f] != (s == StateInert) {
			t.Errorf("%s: caller_code %v", f, marked[f])
		}
	}
	for _, tg := range r.Triggers {
		if tg.File != "main.tf" && tg.Category != CatLiteral {
			t.Errorf("%s: category %s, want %s", tg.ID, tg.Category, CatLiteral)
		}
	}
	tr, _ := states(evalWith(t, r, ladder(t, "", `{"class":"none","examples":[]}`)))
	for _, s := range evalWith(t, r, ladder(t, "", `{"class":"none","examples":[]}`)).Triggers {
		if tr[s.ID] == StateInert && s.Reason != callerReason {
			t.Errorf("%s: reason %q, want %q", s.ID, s.Reason, callerReason)
		}
	}
}

// TestCallerLiteralFailsClosed: caller code a configuration outside the
// caller directories may load, a source the seal cannot place, a read site
// that may read the file, an incomplete read-site scan, an unparsed
// configuration file, another category in the example and a literal moved
// to another file of the example are never marked.
func TestCallerLiteralFailsClosed(t *testing.T) {
	ex := "examples/complete/main.tf"
	cases := []struct {
		name       string
		base, head map[string]string
	}{
		{"root calls the example", with(callerTree("a"), "reuse.tf", "module \"ex\" {\n  source = \"./examples/complete\"\n}\n"),
			with(callerTree("b"), "reuse.tf", "module \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"wrapper calls the tests dir at the base only", with(callerTree("a"), "wrappers/main.tf", "module \"w\" {\n  source = \"../tests/basic\"\n}\n"),
			callerTree("b")},
		{"remote subdirectory of examples", with(callerTree("a"), "reuse.tf", "module \"ex\" {\n  source = \"git::https://example.com/m.git//examples/complete\"\n}\n"),
			with(callerTree("b"), "reuse.tf", "module \"ex\" {\n  source = \"git::https://example.com/m.git//examples/complete\"\n}\n")},
		{"source escapes the tree", with(callerTree("a"), "reuse.tf", "module \"ex\" {\n  source = \"../m/examples/complete\"\n}\n"),
			with(callerTree("b"), "reuse.tf", "module \"ex\" {\n  source = \"../m/examples/complete\"\n}\n")},
		{"read site reaches the example", with(callerTree("a"), "locals.tf", "locals {\n  x = file(\"${path.module}/examples/complete/main.tf\")\n}\n"),
			with(callerTree("b"), "locals.tf", "locals {\n  x = file(\"${path.module}/examples/complete/main.tf\")\n}\n")},
		{"override file calls the example", with(callerTree("a"), "override.tf", "module \"ex\" {\n  source = \"./examples/complete\"\n}\n"),
			with(callerTree("b"), "override.tf", "module \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"read-site scan incomplete", with(callerTree("a"), "extra.tf.json", "{}\n"), with(callerTree("b"), "extra.tf.json", "{}\n")},
		{"root call at the head only", callerTree("a"), with(callerTree("b"), "reuse.tf", reuse("./examples/complete"))},
		{"backslash local source", with(callerTree("a"), "reuse.tf", reuse(`.\\examples\\complete`)), with(callerTree("b"), "reuse.tf", reuse(`.\\examples\\complete`))},
		{"case variant local source", with(callerTree("a"), "reuse.tf", reuse("./Examples/complete")), with(callerTree("b"), "reuse.tf", reuse("./Examples/complete"))},
		{"case variant remote segment", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//Tests/basic")),
			with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//Tests/basic"))},
		{"local source to a missing directory", with(callerTree("a"), "reuse.tf", reuse("./modules/gone")), with(callerTree("b"), "reuse.tf", reuse("./modules/gone"))},
		{"local source leaves the tree", with(callerTree("a"), "reuse.tf", reuse("../sibling")), with(callerTree("b"), "reuse.tf", reuse("../sibling"))},
		{"absolute source", with(callerTree("a"), "reuse.tf", reuse("/srv/m")), with(callerTree("b"), "reuse.tf", reuse("/srv/m"))},
		{"source not a literal", with(callerTree("a"), "reuse.tf", "module \"ex\" {\n  source = local.s\n}\n"),
			with(callerTree("b"), "reuse.tf", "module \"ex\" {\n  source = local.s\n}\n")},
		{"no source", with(callerTree("a"), "reuse.tf", "module \"ex\" {}\n"), with(callerTree("b"), "reuse.tf", "module \"ex\" {}\n")},
		{"OpenTofu file outside", with(callerTree("a"), "main.tofu", "\n"), with(callerTree("b"), "main.tofu", "\n")},
		{"OpenTofu JSON file outside", with(callerTree("a"), "main.tofu.json", "{}\n"), with(callerTree("b"), "main.tofu.json", "{}\n")},
		{"glob star in a remote subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//ex*/complete")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//ex*/complete"))},
		{"glob class in a remote subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//[e]xamples/complete")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//[e]xamples/complete"))},
		{"glob braces in a remote subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//{examples,x}/complete")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//{examples,x}/complete"))},
		{"registry glob subdirectory", with(callerTree("a"), "reuse.tf", reuse("org/self/aws//*/complete")), with(callerTree("b"), "reuse.tf", reuse("org/self/aws//*/complete"))},
		{"short name in a remote subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//EXAMPL~1/complete")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//EXAMPL~1/complete"))},
		{"trailing dot in a remote subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//examples./complete")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//examples./complete"))},
		{"query after a caller subdirectory", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git//tests?ref=v1")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git//tests?ref=v1"))},
		{"caller name after a colon", with(callerTree("a"), "reuse.tf", reuse("git::ssh://git@example.com:examples/m.git")), with(callerTree("b"), "reuse.tf", reuse("git::ssh://git@example.com:examples/m.git"))},
		{"caller name after an equals sign", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git?ref=examples")), with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git?ref=examples"))},
		{"caller name after an ampersand", with(callerTree("a"), "reuse.tf", reuse("git::https://example.com/m.git?depth=1&examples")),
			with(callerTree("b"), "reuse.tf", reuse("git::https://example.com/m.git?depth=1&examples"))},
		{"glob in a local path", with(callerTree("a"), "reuse.tf", reuse("./modules/*")), with(callerTree("b"), "reuse.tf", reuse("./modules/*"))},
		{"Stacks or Terragrunt file outside: main.tfcomponent.hcl", with(callerTree("a"), "main.tfcomponent.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n"), with(callerTree("b"), "main.tfcomponent.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"Stacks or Terragrunt file outside: main.tfstack.hcl", with(callerTree("a"), "main.tfstack.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n"), with(callerTree("b"), "main.tfstack.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"Stacks or Terragrunt file outside: main.tfdeploy.hcl", with(callerTree("a"), "main.tfdeploy.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n"), with(callerTree("b"), "main.tfdeploy.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"Stacks or Terragrunt file outside: main.tfcomponent.json", with(callerTree("a"), "main.tfcomponent.json", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n"), with(callerTree("b"), "main.tfcomponent.json", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
		{"Stacks or Terragrunt file outside: main.tfstack.json", with(callerTree("a"), "main.tfstack.json", "{}\n"), with(callerTree("b"), "main.tfstack.json", "{}\n")},
		{"Stacks or Terragrunt file outside: main.tfdeploy.json", with(callerTree("a"), "main.tfdeploy.json", "{}\n"), with(callerTree("b"), "main.tfdeploy.json", "{}\n")},
		{"Stacks or Terragrunt file outside: terragrunt.hcl", with(callerTree("a"), "terragrunt.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n"), with(callerTree("b"), "terragrunt.hcl", "component \"ex\" {\n  source = \"./examples/complete\"\n}\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, c.base, c.head, "")
			state, marked := byFile(t, r)
			if state[ex] != StateUnverifiable || marked[ex] {
				t.Errorf("%s: %s caller_code %v, want unverifiable and unmarked\n%s", ex, state[ex], marked[ex], mustJSON(t, r))
			}
		})
	}

	// Another category in an example keeps its state.
	head := with(callerTree("a"), ex, "module \"s3\" {\n  source = \"../../\"\n  name   = \"a\"\n}\n\nresource \"aws_s3_bucket\" \"extra\" {\n  bucket = \"x\"\n}\n")
	r := runPair(t, callerTree("a"), head, "")
	for _, tg := range r.Triggers {
		if tg.CallerCode {
			t.Errorf("%s %s: caller_code on another category\n%s", tg.ID, tg.Category, mustJSON(t, r))
		}
	}

	// A literal changed while its block moves to another file of the
	// example: the file is added, not modified in place.
	head = with(callerTree("a"), ex, "", "examples/complete/call.tf", "module \"s3\" {\n  source = \"../../\"\n  name   = \"b\"\n}\n")
	r = runPair(t, callerTree("a"), head, "")
	lit := false
	for _, tg := range r.Triggers {
		lit = lit || tg.File == "examples/complete/call.tf" && tg.Category == CatLiteral
		if tg.CallerCode {
			t.Errorf("moved: %s %s marked\n%s", tg.ID, tg.File, mustJSON(t, r))
		}
	}
	if !lit {
		t.Errorf("moved: no literal trigger in call.tf\n%s", mustJSON(t, r))
	}
}

// reuse is a root module call of src.
func reuse(src string) string {
	return "module \"ex\" {\n  source = \"" + src + "\"\n}\n"
}

// TestCallerSealLinks: a link outside the caller directories withholds the
// mark, whether a module source walks through it or not.
func TestCallerSealLinks(t *testing.T) {
	cases := map[string]struct{ link, target, call string }{
		"module source through a directory link": {"modules/alias", "../examples/complete", "./modules/alias"},
		"directory link with no module call":     {"modules/alias", "../examples/complete", ""},
		"source through a linked parent":         {"ex", "examples", "./ex/complete"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			files := func(v string) map[string]string {
				if c.call == "" {
					return callerTree(v)
				}
				return with(callerTree(v), "reuse.tf", reuse(c.call))
			}
			base, head := writeTree(t, files("a")), writeTree(t, files("b"))
			for _, root := range []string{base, head} {
				p := filepath.Join(root, filepath.FromSlash(c.link))
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(c.target, p); err != nil {
					t.Skip("no symlinks:", err)
				}
			}
			r, err := Run(Options{Head: head, Base: base, Limits: facts.DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			state, marked := byFile(t, r)
			if ex := "examples/complete/main.tf"; state[ex] != StateUnverifiable || marked[ex] {
				t.Errorf("%s: %s caller_code %v\n%s", ex, state[ex], marked[ex], mustJSON(t, r))
			}
		})
	}
}

// TestCallerSealDeadline: a parse past the deadline withholds the mark.
func TestCallerSealDeadline(t *testing.T) {
	tree := func(v string) *facts.Tree {
		tr, err := facts.ReadTree(writeTree(t, callerTree(v)), facts.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	c := &classifier{base: tree("a"), head: tree("b"), lim: facts.DefaultLimits}
	if !c.callerSealed() {
		t.Fatal("not sealed before the deadline")
	}
	c.lim.Deadline = time.Now().Add(-time.Second)
	if c.callerSealed() {
		t.Error("sealed past the deadline")
	}
}

// TestMarkCallerCodeGuards: the trigger and entry guards of the mark, on a
// sealed tree with a complete scan.
func TestMarkCallerCodeGuards(t *testing.T) {
	tree := func(v string) *facts.Tree {
		tr, err := facts.ReadTree(writeTree(t, callerTree(v)), facts.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	c := &classifier{base: tree("a"), head: tree("b"), lim: facts.DefaultLimits}
	ex := "examples/complete/main.tf"
	file, link := &facts.Entry{Kind: facts.KindFile}, &facts.Entry{Kind: facts.KindSymlink}
	lit := func(mod func(*Trigger)) Trigger {
		tg := Trigger{File: ex, Category: CatLiteral, Levels: []string{LevelL4}}
		mod(&tg)
		return tg
	}
	cases := []struct {
		name string
		tg   Trigger
		ch   change
		want bool
	}{
		{"marked", lit(func(*Trigger) {}), change{path: ex, kind: KindModified, h: file, b: file}, true},
		{"merged other category", lit(func(tg *Trigger) { tg.AlsoCategories = []string{CatUnknownPropagation} }), change{path: ex, kind: KindModified, h: file, b: file}, false},
		{"follower", lit(func(tg *Trigger) { tg.Follows = []string{} }), change{path: ex, kind: KindModified, h: file, b: file}, false},
		{"other category", lit(func(tg *Trigger) { tg.Category = CatResourceAddRemove }), change{path: ex, kind: KindModified, h: file, b: file}, false},
		{"head a link", lit(func(*Trigger) {}), change{path: ex, kind: KindModified, h: link, b: file}, false},
		{"base a link", lit(func(*Trigger) {}), change{path: ex, kind: KindModified, h: file, b: link}, false},
		{"renamed", lit(func(*Trigger) {}), change{path: ex, prev: "examples/old/main.tf", kind: KindRenamed, h: file, b: file}, false},
		{"added", lit(func(*Trigger) {}), change{path: ex, kind: KindAdded, h: file}, false},
	}
	for _, tc := range cases {
		ts := []Trigger{tc.tg}
		c.markCallerCode(ts, []change{tc.ch}, &scan{complete: true, roots: []string{"."}})
		if ts[0].CallerCode != tc.want {
			t.Errorf("%s: caller_code %v, want %v", tc.name, ts[0].CallerCode, tc.want)
		}
	}
}

// TestCallerLiteralLink: an example .tf file that is a link is never marked.
func TestCallerLiteralLink(t *testing.T) {
	base, head := writeTree(t, callerTree("a")), writeTree(t, callerTree("b"))
	for _, root := range []string{base, head} {
		p := filepath.Join(root, "examples", "complete", "main.tf")
		if err := os.Rename(p, p+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("main.tf.real", p); err != nil {
			t.Skip("no symlinks:", err)
		}
	}
	r, err := Run(Options{Head: head, Base: base, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range r.Triggers {
		if tg.CallerCode {
			t.Errorf("%s %s marked through a link\n%s", tg.ID, tg.File, mustJSON(t, r))
		}
	}
}

// TestCallerLiteralEval: claims-eval takes caller_code only on a literal
// argument trigger of a caller .tf file that follows nothing, and never over
// a refuted claim.
func TestCallerLiteralEval(t *testing.T) {
	line := 3
	mk := func(id, file string, also ...string) Trigger {
		return Trigger{ID: id, File: file, Line: &line, Category: CatLiteral, AlsoCategories: also,
			Address: new("module.s3"), Attribute: new("name"), Levels: []string{LevelL4}, CallerCode: true}
	}
	b := &Report{SchemaVersion: 1, Entries: []Entry{}, Triggers: []Trigger{
		mk("T-001", "examples/complete/main.tf"),
		mk("T-002", "tests/a/b/main.tf", CatLiteral),
		mk("T-003", "examples/complete/main.tf", CatWrapper),
		mk("T-004", "main.tf"),
		mk("T-005", "examples/../main.tf"),
		mk("T-006", "tests/basic.tftest.hcl"),
		mk("T-007", "examples/complete/main.tf"),
		mk("T-008", "examples/complete/main.tf"),
		mk("T-009", "docs/examples/main.tf"),
		mk("T-010", "examples"),
		mk("T-011", "examples/complete/main.tf"),
	}}
	b.Triggers[10].Category, b.Triggers[10].AlsoCategories = CatWrapper, []string{CatLiteral}
	b.Triggers[7].Follows = []string{"T-004"}
	b.Triggers[7].Levels = []string{}
	b.Triggers[7].Reason = new("follows")
	r := evalWith(t, b, ladder(t, "", noCoverage),
		claim("C-1", PredPlanAction, `{"node":"T-007","revision":"head","action":"update"}`))
	tr, _ := states(r)
	want := map[string]string{"T-001": StateInert, "T-002": StateInert, "T-003": StateUnverifiable, "T-004": StateUnverifiable,
		"T-005": StateUnverifiable, "T-006": StateUnverifiable, "T-007": StateInert, "T-008": StateUnverifiable,
		"T-009": StateUnverifiable, "T-010": StateUnverifiable, "T-011": StateUnverifiable}
	for id, s := range want {
		if tr[id] != s {
			t.Errorf("%s: %s, want %s\n%s", id, tr[id], s, mustJSON(t, r))
		}
	}

	// A refuted claim is never turned into inert.
	pr := pr556TwoTriggers(t)
	pr.Triggers[1].File, pr.Triggers[1].Category, pr.Triggers[1].AlsoCategories = "examples/complete/main.tf", CatLiteral, nil
	pr.Triggers[1].CallerCode = true
	r = evalWith(t, pr, ladder(t, l1Runs+","+l3Run, noCoverage),
		claim("C-8", PredPlanAction, `{"node":"T-002","revision":"head","action":"no-op"}`))
	tr, cl := states(r)
	if cl["C-8"] != StateRefuted || tr["T-002"] != StateRefuted {
		t.Errorf("refuted: triggers %v claims %v\n%s", tr, cl, mustJSON(t, r))
	}
}

// TestCallerCodeExample: the report of a change to one literal in an
// example is the schema example behaviour.valid.caller-code.json; it reads
// back, and claims-eval ends the trigger inert with no ladder run.
func TestCallerCodeExample(t *testing.T) {
	head := callerTree("a")
	head["examples/complete/main.tf"] = "module \"s3\" {\n  source = \"../../\"\n  name   = \"b\"\n}\n"
	got := mustJSON(t, runPair(t, callerTree("a"), head, ""))
	example := "../../schema/examples/behaviour.valid.caller-code.json"
	if *update {
		if err := os.WriteFile(example, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if e, err := os.ReadFile(example); err != nil || !bytes.Equal(e, got) {
		t.Errorf("%s differs from the report (go test -update rewrites it):\n%s", example, got)
	}
	b, err := ReadBehaviour(writeJSON(t, "behaviour.json", string(got)))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Triggers) != 1 || !b.Triggers[0].CallerCode {
		t.Fatalf("want one caller_code trigger:\n%s", got)
	}
	if tr := evalNoLadder(t, b); tr["T-001"] != StateInert {
		t.Errorf("T-001: %s, want inert", tr["T-001"])
	}
}

// outside are files outside the caller directories, the same at both
// revisions, each of which withholds the mark: a module source that is not
// a registry address or a local directory of the tree, a path the tree
// reader skips, a submodule, and an HCL file the seal does not read.
var outside = map[string][2]string{
	"git source":                       {"reuse.tf", reuse("git::https://example.com/org/m.git//modules/x?ref=v1.0.0")},
	"trailing space in a remote path":  {"reuse.tf", reuse("git::https://example.com/m.git//examples /complete")},
	"github shorthand":                 {"reuse.tf", reuse("github.com/org/m//modules/x")},
	"http archive":                     {"reuse.tf", reuse("https://example.com/m.zip")},
	"file URL":                         {"reuse.tf", reuse("file:///srv/m")},
	"forced file getter":               {"reuse.tf", reuse("file::/srv/m")},
	"drive letter":                     {"reuse.tf", reuse("C:/srv/m")},
	"s3 source":                        {"reuse.tf", reuse("s3::https://s3.amazonaws.com/b/m.zip")},
	"registry with a host":             {"reuse.tf", reuse("app.terraform.io/org/m/aws")},
	"registry subdirectory of tests":   {"reuse.tf", reuse("org/self/aws//tests/basic")},
	"registry hidden subdirectory":     {"reuse.tf", reuse("org/self/aws//modules/.terraform")},
	"registry dot subdirectory":        {"reuse.tf", reuse("org/self/aws//modules/..")},
	"local double slash":               {"reuse.tf", reuse("./modules//x")},
	"local directory with no .tf file": {"reuse.tf", reuse("./docs")},
	"tracked .terraform directory":     {"modules/.terraform/main.tf", reuse("../../examples/complete")},
	"nested .git file":                 {"modules/sub/.git", "gitdir: ../../.git/modules/sub\n"},
	".gitmodules":                      {".gitmodules", "[submodule \"sub\"]\n\tpath = modules/sub\n"},
	"root.hcl":                         {"root.hcl", "x = 1\n"},
	"nested unit.hcl":                  {"live/unit.hcl", "x = 1\n"},
	"Terragrunt.hcl":                   {"Terragrunt.hcl", "x = 1\n"},
	"upper-case suffix":                {"ROOT.HCL", "x = 1\n"},
	"terragrunt.hcl.json":              {"terragrunt.hcl.json", "{}\n"},
	"terragrunt.stack.hcl":             {"terragrunt.stack.hcl", "x = 1\n"},
}

// sealed are files outside the caller directories that keep the mark.
var sealed = map[string][2]string{
	"registry address":              {"reuse.tf", reuse("terraform-aws-modules/s3-bucket/aws")},
	"registry submodule":            {"reuse.tf", reuse("org/m/aws//modules/x")},
	"local submodule":               {"reuse.tf", reuse("./modules/x")},
	"test file at the root":         {"main.tftest.hcl", "run \"plan\" {\n  command = plan\n}\n"},
	"tflint configuration":          {".tflint.hcl", "plugin \"aws\" {\n  enabled = true\n}\n"},
	"dependency lock file":          {".terraform.lock.hcl", "# lock\n"},
	".terraform under the examples": {"examples/complete/.terraform/modules/modules.json", "{}\n"},
	"top-level .git directory":      {".git/config", "[core]\n"},
	"top-level .git file":           {".git", "gitdir: /srv/repo/.git/worktrees/x\n"},
}

// TestCallerSealAllowlist: only a registry address or a local directory of
// the tree keeps the seal, and a skipped path, a submodule or an unread HCL
// file breaks it.
func TestCallerSealAllowlist(t *testing.T) {
	ex := "examples/complete/main.tf"
	sub := map[string]string{"modules/x/main.tf": "variable \"n\" {\n  type = string\n}\n", "docs/README.md": "x\n"}
	run := func(t *testing.T, f [2]string) (string, bool) {
		t.Helper()
		tree := func(v string) map[string]string {
			m := with(callerTree(v), f[0], f[1])
			maps.Copy(m, sub)
			return m
		}
		state, marked := byFile(t, runPair(t, tree("a"), tree("b"), ""))
		return state[ex], marked[ex]
	}
	for name, f := range outside {
		t.Run("withheld/"+name, func(t *testing.T) {
			if s, m := run(t, f); s != StateUnverifiable || m {
				t.Errorf("%s: %s caller_code %v, want unverifiable and unmarked", ex, s, m)
			}
		})
	}
	for name, f := range sealed {
		t.Run("sealed/"+name, func(t *testing.T) {
			if s, m := run(t, f); s != StateInert || !m {
				t.Errorf("%s: %s caller_code %v, want inert and marked", ex, s, m)
			}
		})
	}
}

// seal4Withheld and seal4Sealed are trees, as extra files beside
// callerTree, that withhold or keep the mark: a local source or a registry
// subdirectory with a segment that is not plain ASCII letters, digits, _
// and -, each local one into a real directory of that name, and a read site
// that names a caller file in another letter case or spelling.
var seal4Withheld = map[string]map[string]string{
	"local ligature":                {"tes\ufb06/basic/x.tf": "variable \"x\" {}\n", "wrappers/main.tf": reuse("../tes\ufb06/basic")},
	"local trailing dot":            {"tests./basic/x.tf": "variable \"x\" {}\n", "wrappers/main.tf": reuse("../tests./basic")},
	"local trailing space":          {"tests /basic/x.tf": "variable \"x\" {}\n", "wrappers/main.tf": reuse("../tests /basic")},
	"local short name":              {"exampl~1/complete/x.tf": "variable \"x\" {}\n", "wrappers/main.tf": reuse("../exampl~1/complete")},
	"local inner dot":               {"modules/x.y/x.tf": "variable \"x\" {}\n", "reuse.tf": reuse("./modules/x.y")},
	"local dot after a segment":     {"modules/x/x.tf": "variable \"x\" {}\n", "reuse.tf": reuse("./modules/x/../x")},
	"registry case variant":         {"reuse.tf": reuse("o/n/aws//Examples/complete")},
	"registry trailing dot":         {"reuse.tf": reuse("o/n/aws//modules/x.")},
	"registry inner trailing dot":   {"reuse.tf": reuse("o/n/aws//x./y")},
	"registry short name":           {"reuse.tf": reuse("o/n/aws//exampl~1")},
	"registry space":                {"reuse.tf": reuse("o/n/aws//examples /complete")},
	"registry hidden segment":       {"reuse.tf": reuse("org/self/aws//.terraform")},
	"registry inner dot":            {"reuse.tf": reuse("o/n/aws//modules/x.y")},
	"read site case variant":        {"locals.tf": "locals {\n  x = file(\"${path.module}/Examples/complete/main.tf\")\n}\n"},
	"read site upper-case file":     {"locals.tf": "locals {\n  x = file(\"${path.module}/examples/Complete/MAIN.TF\")\n}\n"},
	"read site templatefile":        {"locals.tf": "locals {\n  x = templatefile(\"${path.module}/Tests/basic/main.tf\", {})\n}\n"},
	"read site with no anchor":      {"locals.tf": "locals {\n  x = file(\"Examples/complete/main.tf\")\n}\n"},
	"read site trailing dot":        {"locals.tf": "locals {\n  x = file(\"${path.module}/examples./complete/main.tf\")\n}\n"},
	"read site short name":          {"locals.tf": "locals {\n  x = file(\"${path.module}/exampl~1/complete/main.tf\")\n}\n"},
	"read site backslash":           {"locals.tf": "locals {\n  x = file(\"${path.module}\\\\examples\\\\complete\\\\main.tf\")\n}\n"},
	"read site from a submodule":    {"modules/x/main.tf": "locals {\n  x = file(\"${path.module}/../../Examples/complete/main.tf\")\n}\n"},
	"local source with no ./":       {"modules/x/x.tf": "variable \"x\" {}\n", "reuse.tf": reuse("modules/x")},
	"read site dynamic part":        {"locals.tf": "variable \"p\" {\n  type = string\n}\n\nlocals {\n  x = file(\"${path.module}/Ex${var.p}\")\n}\n"},
	"read site of a shell command":  {"cmd.tf": "resource \"terraform_data\" \"c\" {\n  provisioner \"local-exec\" {\n    command = \"cat notes.txt\"\n  }\n}\n"},
	"read site trailing space":      {"locals.tf": "locals {\n  x = file(\"${path.module}/examples /complete/main.tf\")\n}\n"},
	"read site of the directory":    {"locals.tf": "locals {\n  x = templatefile(\"${path.module}/Examples/complete\", {})\n}\n"},
	"read site leaves the tree":     {"locals.tf": "locals {\n  x = file(\"${path.module}/../notes.txt\")\n}\n"},
	"read site of a non-ASCII name": {"locals.tf": "locals {\n  x = file(\"${path.module}/tes\ufb06/basic/main.tf\")\n}\n"},
}

var seal4Sealed = map[string]map[string]string{
	"wrapper calls the module root": {"wrappers/main.tf": reuse("../")},
	"wrapper calls a submodule":     {"modules/x/main.tf": "variable \"n\" {}\n", "wrappers/main.tf": reuse("../modules/x")},
	"example reads its own file":    {"examples/complete/data.tf": "locals {\n  x = file(\"${path.module}/user_data.sh\")\n}\n", "examples/complete/user_data.sh": "#!/bin/sh\n"},
	"module reads its own file":     {"locals.tf": "locals {\n  x = file(\"${path.module}/policy.json\")\n}\n", "policy.json": "{}\n"},
}

// TestCallerSealSegments: see seal4Withheld and seal4Sealed.
func TestCallerSealSegments(t *testing.T) {
	ex := "examples/complete/main.tf"
	run := func(t *testing.T, extra map[string]string) (string, bool) {
		t.Helper()
		tree := func(v string) map[string]string {
			m := callerTree(v)
			maps.Copy(m, extra)
			return m
		}
		state, marked := byFile(t, runPair(t, tree("a"), tree("b"), ""))
		return state[ex], marked[ex]
	}
	for name, extra := range seal4Withheld {
		t.Run("withheld/"+name, func(t *testing.T) {
			if s, m := run(t, extra); s != StateUnverifiable || m {
				t.Errorf("%s: %s caller_code %v, want unverifiable and unmarked", ex, s, m)
			}
		})
	}
	for name, extra := range seal4Sealed {
		t.Run("sealed/"+name, func(t *testing.T) {
			if s, m := run(t, extra); s != StateInert || !m {
				t.Errorf("%s: %s caller_code %v, want inert and marked", ex, s, m)
			}
		})
	}
}

// TestCallerSealLocalCaseVariant: on a file system that keeps Examples/
// and examples/ apart, a local source into Examples/ withholds the mark.
func TestCallerSealLocalCaseVariant(t *testing.T) {
	files := with(callerTree("a"), "Examples/complete/x.tf", "variable \"x\" {}\n", "reuse.tf", reuse("./Examples/complete"))
	base := writeTree(t, files)
	if _, err := os.Stat(filepath.Join(base, "examples", "complete", "x.tf")); err == nil {
		t.Skip("the file system folds case")
	}
	files["examples/complete/main.tf"] = "module \"s3\" {\n  source = \"../../\"\n  name   = \"b\"\n}\n"
	r, err := Run(Options{Head: writeTree(t, files), Base: base, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if state, marked := byFile(t, r); marked["examples/complete/main.tf"] {
		t.Errorf("marked: %s\n%s", state["examples/complete/main.tf"], mustJSON(t, r))
	}
}

// TestCallerSealDiffMode: with --diff, a diff path under a skipped
// directory or a removed .gitmodules withholds the mark; the same change
// with no such path keeps it.
func TestCallerSealDiffMode(t *testing.T) {
	head := with(callerTree("a"), "examples/complete/main.tf", "module \"s3\" {\n  source = \"../../\"\n  name   = \"b\"\n}\n")
	lit := "diff --git a/examples/complete/main.tf b/examples/complete/main.tf\n" +
		"--- a/examples/complete/main.tf\n+++ b/examples/complete/main.tf\n" +
		"@@ -3 +3 @@\n-  name   = \"a\"\n+  name   = \"b\"\n"
	removed := func(p string, lines ...string) string {
		d := "diff --git a/" + p + " b/" + p + "\ndeleted file mode 100644\n--- a/" + p + "\n+++ /dev/null\n" +
			fmt.Sprintf("@@ -1,%d +0,0 @@\n", len(lines))
		for _, l := range lines {
			d += "-" + l + "\n"
		}
		return d
	}
	loader := []string{"module \"e\" {", "  source = \"../../examples/complete\"", "}"}
	cases := []struct {
		name   string
		diff   string
		marked bool
	}{
		{"literal only", lit, true},
		{"removed .terraform loader", lit + removed("modules/.terraform/main.tf", loader...), false},
		{"removed nested .git file", lit + removed("modules/sub/.git", "gitdir: ../../.git/modules/sub"), false},
		{"removed .gitmodules", lit + removed(".gitmodules", "[submodule \"sub\"]"), false},
	}
	root := writeTree(t, head)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := Run(Options{Head: root, Diff: writeJSON(t, "change.diff", c.diff), Limits: facts.DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			if _, marked := byFile(t, r); marked["examples/complete/main.tf"] != c.marked {
				t.Errorf("caller_code %v, want %v\n%s", marked["examples/complete/main.tf"], c.marked, mustJSON(t, r))
			}
		})
	}
}

// TestCallerSiteFolds: a read site is compared with the caller file in any
// letter case on both sides, and a module-anchored site from its own
// directory.
func TestCallerSiteFolds(t *testing.T) {
	call := func(v string) string { return "module \"s3\" {\n  source = \"../../\"\n  name   = \"" + v + "\"\n}\n" }
	cases := []struct {
		name       string
		file       string
		base, head map[string]string
	}{
		{"upper-case caller directory read in lower case", "examples/Complete/main.tf",
			map[string]string{"main.tf": "locals {\n  x = file(\"${path.module}/examples/complete/main.tf\")\n}\n", "examples/Complete/main.tf": call("a")},
			map[string]string{"main.tf": "locals {\n  x = file(\"${path.module}/examples/complete/main.tf\")\n}\n", "examples/Complete/main.tf": call("b")}},
		{"test configuration reads its own file", "tests/basic/main.tf",
			map[string]string{"main.tf": "\n", "tests/basic/data.tf": "locals {\n  x = file(\"${path.module}/main.tf\")\n}\n", "tests/basic/main.tf": call("a")},
			map[string]string{"main.tf": "\n", "tests/basic/data.tf": "locals {\n  x = file(\"${path.module}/main.tf\")\n}\n", "tests/basic/main.tf": call("b")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, c.base, c.head, "")
			state, marked := byFile(t, r)
			if state[c.file] != StateUnverifiable || marked[c.file] {
				t.Errorf("%s: %s caller_code %v\n%s", c.file, state[c.file], marked[c.file], mustJSON(t, r))
			}
		})
	}
}
