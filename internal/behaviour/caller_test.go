package behaviour

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

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

// TestCallerLiteralFailsClosed: caller code reused as a module, a read site
// that may read the file, an incomplete read-site scan, another category in
// the example and a file renamed from outside the caller directories stay
// unverifiable or keep their category.
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

	// A module file renamed into an example keeps its triggers.
	base := with(callerTree("a"), "other.tf", "module \"s3\" {\n  source = \"./\"\n  name   = \"a\"\n}\n")
	head = callerTree("a")
	head["examples/complete/main.tf"] = "module \"s3\" {\n  source = \"./\"\n  name   = \"b\"\n}\n"
	delete(base, "examples/complete/main.tf")
	r = runPair(t, base, head, "")
	for _, tg := range r.Triggers {
		if tg.CallerCode {
			t.Errorf("renamed: %s %s marked\n%s", tg.ID, tg.File, mustJSON(t, r))
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
	}}
	b.Triggers[7].Follows = []string{"T-004"}
	b.Triggers[7].Levels = []string{}
	b.Triggers[7].Reason = new("follows")
	r := evalWith(t, b, ladder(t, "", noCoverage),
		claim("C-1", PredPlanAction, `{"node":"T-007","revision":"head","action":"update"}`))
	tr, _ := states(r)
	want := map[string]string{"T-001": StateInert, "T-002": StateInert, "T-003": StateUnverifiable, "T-004": StateUnverifiable,
		"T-005": StateUnverifiable, "T-006": StateUnverifiable, "T-007": StateInert, "T-008": StateUnverifiable,
		"T-009": StateUnverifiable, "T-010": StateUnverifiable}
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
