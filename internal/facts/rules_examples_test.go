package facts

import (
	"fmt"
	"maps"
	"strings"
	"testing"
)

func runBase(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	r, err := Run(Options{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const disabledRule = "examples.disabled-call-missing"

func TestDisabledCallShapes(t *testing.T) {
	const call = "module \"this\" {\n  source = \"../../\"\n}\n"
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"literal false":       {map[string]string{"main.tf": call + "module \"disabled\" {\n  source = \"../../\"\n  create = false\n}\n"}, "clean"},
		"in another file":     {map[string]string{"main.tf": call, "disabled.tf": "module \"disabled\" {\n  create = false\n}\n"}, "clean"},
		"with count":          {map[string]string{"main.tf": "module \"disabled\" {\n  count  = 1\n  create = false\n}\n"}, "clean"},
		"no call":             {map[string]string{"main.tf": call}, "candidate"},
		"no create":           {map[string]string{"main.tf": "module \"disabled\" {\n  source = \"../../\"\n}\n"}, "candidate"},
		"create true":         {map[string]string{"main.tf": "module \"disabled\" {\n  create = true\n}\n"}, "candidate"},
		"create expression":   {map[string]string{"main.tf": "module \"disabled\" {\n  create = !true\n}\n"}, "candidate"},
		"create string":       {map[string]string{"main.tf": "module \"disabled\" {\n  create = \"false\"\n}\n"}, "candidate"},
		"other name":          {map[string]string{"main.tf": "module \"this_disabled\" {\n  create = false\n}\n"}, "candidate"},
		"create on a sibling": {map[string]string{"main.tf": "module \"this\" {\n  create = false\n}\nmodule \"disabled\" {}\n"}, "candidate"},
		"parse error":         {map[string]string{"main.tf": "module \"disabled\" {\n"}, "not_evaluated"},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{}
			for p, body := range tc.files {
				files["examples/complete/"+p] = body
			}
			s := statusOf(runHead(t, files).Manifest, disabledRule, "examples/complete")
			if s != tc.want && !strings.HasPrefix(s, tc.want+": ") {
				t.Errorf("got %s, want %s", s, tc.want)
			}
		})
	}
}

// Only the complete example is judged: any other example is clean without
// a disabled call, and a complete example the change removes is clean too.
func TestDisabledCallOnlyInComplete(t *testing.T) {
	r := runHead(t, map[string]string{"examples/basic/main.tf": "module \"this\" {}\n", "examples/completely/main.tf": ""})
	for _, area := range []string{"examples/basic", "examples/completely"} {
		if s := statusOf(r.Manifest, disabledRule, area); s != "clean" {
			t.Errorf("%s: %s", area, s)
		}
	}
	r = runBase(t, map[string]string{"examples/complete/main.tf": "module \"this\" {}\n"}, map[string]string{"main.tf": ""})
	if s := statusOf(r.Manifest, disabledRule, "examples/complete"); s != "clean" {
		t.Errorf("removed complete example: %s", s)
	}
}

func TestDisabledCallEvidenceCap(t *testing.T) {
	long := strings.Repeat("x", 2*maxEvidenceText)
	r := runHead(t, map[string]string{"examples/complete/main.tf": "module \"disabled\" {\n  create = var." + long + "\n}\n"})
	cs := candidatesOf(r, disabledRule)
	if len(cs) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := cs[0]
	if c.EvidenceComplete || len(c.Evidence.SourceLines) != 2 || len(c.Evidence.SourceLines[1].Text) != maxEvidenceText {
		t.Errorf("cap did not bite: complete=%v lines=%+v", c.EvidenceComplete, c.Evidence.SourceLines)
	}
	if c.Line == nil || *c.Line != 1 || c.Evidence.Head == nil || c.Evidence.Base != nil {
		t.Errorf("candidate %+v", c)
	}
}

const submoduleRule = "examples.submodule-missing"

func TestSubmoduleMissingCases(t *testing.T) {
	const sub = "variable \"x\" {\n  type = string\n}\n"
	base := map[string]string{"main.tf": "", "modules/old/main.tf": ""}
	for name, tc := range map[string]struct {
		head map[string]string
		want string
	}{
		"own example":              {map[string]string{"modules/new/main.tf": sub, "examples/new/main.tf": ""}, "clean"},
		"no example":               {map[string]string{"modules/new/main.tf": sub}, "candidate"},
		"example is a file":        {map[string]string{"modules/new/main.tf": sub, "examples/new": ""}, "candidate"},
		"example is a link":        {map[string]string{"modules/new/main.tf": sub, "examples/x/main.tf": "", "examples/new": "->x"}, "candidate"},
		"existing submodule":       {map[string]string{"modules/old/main.tf": sub}, "clean"},
		"removed submodule":        {map[string]string{}, "clean"},
		"unparsable is still read": {map[string]string{"modules/new/main.tf": "variable {"}, "candidate"},
	} {
		t.Run(name, func(t *testing.T) {
			head := map[string]string{"main.tf": ""}
			if name != "removed submodule" {
				head["modules/old/main.tf"] = ""
			}
			maps.Copy(head, tc.head)
			if s := statusOf(runBase(t, base, head).Manifest, submoduleRule, "."); s != tc.want {
				t.Errorf("got %s, want %s", s, tc.want)
			}
		})
	}
}

// Without a base the rule is no_base, even with no submodule in the tree.
func TestSubmoduleMissingNoBase(t *testing.T) {
	for _, files := range []map[string]string{{"main.tf": ""}, {"main.tf": "", "modules/a/main.tf": ""}} {
		if s := statusOf(runHead(t, files).Manifest, submoduleRule, "."); s != "not_evaluated: no_base" {
			t.Error(s)
		}
	}
}

func TestSubmoduleMissingEvidence(t *testing.T) {
	var calls strings.Builder
	for range maxEvidenceComments + 1 {
		calls.WriteString("  source = \"../../modules/new\"\n")
	}
	head := map[string]string{
		"main.tf": "", "modules/new/main.tf": "",
		"examples/a/main.tf": "source = \"../../modules/newer\"\nsource = \"../../modules/new/sub\"\n",
		"examples/b/main.tf": calls.String(),
	}
	cs := candidatesOf(runBase(t, map[string]string{"main.tf": ""}, head), submoduleRule)
	if len(cs) != 1 {
		t.Fatalf("candidates %+v", cs)
	}
	c := cs[0]
	ls := c.Evidence.SourceLines
	if c.EvidenceComplete || len(ls) != maxEvidenceComments {
		t.Fatalf("cap did not bite: complete=%v lines=%d", c.EvidenceComplete, len(ls))
	}
	// modules/newer does not name modules/new; modules/new/sub does.
	if ls[0].File != "examples/a/main.tf" || ls[0].Line != 2 || ls[1].File != "examples/b/main.tf" {
		t.Errorf("lines %+v", ls[:2])
	}
	if c.File != "modules/new" || c.Line != nil || c.Subject != "modules/new" || *c.Evidence.Head != "modules/new" || c.Evidence.Base != nil {
		t.Errorf("candidate %+v", c)
	}
}

func TestReferencesPath(t *testing.T) {
	for l, want := range map[string]bool{
		`source = "../../modules/x"`:     true,
		`source = "../../modules/x/sub"`: true,
		`../../modules/x`:                true,
		`source = "../../modules/xy"`:    false,
		`modules/xy and modules/x"`:      true,
		`modules/xy and modules/xz`:      false,
		``:                               false,
	} {
		if got := referencesPath([]byte(l), []byte("modules/x")); got != want {
			t.Errorf("%q: got %v", l, got)
		}
	}
}

const staleRule = "examples.stale-reference"

func TestStaleReferenceCases(t *testing.T) {
	const out = "output \"gone\" {\n  value = \"g\"\n}\n"
	base := map[string]string{"main.tf": "", "outputs.tf": out, "modules/sub/outputs.tf": out,
		"examples/e/main.tf": "module \"this\" {\n  source = \"../../\"\n}\nmodule \"sub\" {\n  source = \"../../modules/sub\"\n}\n"}
	for name, tc := range map[string]struct {
		head map[string]string // replaces or adds base paths
		want string
		file string
	}{
		"nothing removed":     {map[string]string{"main.tf": "# x\n"}, "clean", ""},
		"unread removal":      {map[string]string{"outputs.tf": ""}, "clean", ""},
		"read removal":        {map[string]string{"outputs.tf": "", "examples/e/o.tf": "output \"x\" {\n  value = module.this.gone\n}\n"}, "candidate", "outputs.tf"},
		"submodule removal":   {map[string]string{"modules/sub/outputs.tf": "", "examples/e/o.tf": "output \"x\" {\n  value = module.sub.gone\n}\n"}, "candidate", "modules/sub/outputs.tf"},
		"indexed call":        {map[string]string{"outputs.tf": "", "examples/e/o.tf": "output \"x\" {\n  value = module.this[0]\n}\n"}, "candidate", "outputs.tf"},
		"longer call name":    {map[string]string{"outputs.tf": "", "examples/e/o.tf": "output \"x\" {\n  value = module.this_other.gone\n}\n"}, "clean", ""},
		"longer output name":  {map[string]string{"outputs.tf": "", "examples/e/o.tf": "output \"x\" {\n  value = module.this.gone_too\n}\n"}, "clean", ""},
		"callee parse error":  {map[string]string{"outputs.tf": "output {", "examples/e/o.tf": "output \"x\" {\n  value = module.this.gone\n}\n"}, "not_evaluated", ""},
		"example parse error": {map[string]string{"outputs.tf": "", "examples/e/o.tf": "output {"}, "not_evaluated", ""},
		"wrapper source":      {map[string]string{"outputs.tf": "", "examples/e/main.tf": "module \"this\" {\n  source = \"../../wrappers\"\n}\noutput \"x\" {\n  value = module.this.gone\n}\n"}, "clean", ""},
	} {
		t.Run(name, func(t *testing.T) {
			head := maps.Clone(base)
			maps.Copy(head, tc.head)
			r := runBase(t, base, head)
			s := statusOf(r.Manifest, staleRule, ".")
			if s != tc.want && !strings.HasPrefix(s, tc.want+": ") {
				t.Fatalf("got %s, want %s", s, tc.want)
			}
			if cs := candidatesOf(r, staleRule); tc.file != "" && (len(cs) != 1 || cs[0].File != tc.file || cs[0].Line != nil) {
				t.Errorf("candidates %+v", cs)
			}
		})
	}
}

// A rename on a line the change does not touch is not in the change: only
// a new output with the removed one's value is a rename, and its line is
// the anchor.
func TestStaleReferenceRename(t *testing.T) {
	base := map[string]string{"outputs.tf": "output \"old\" {\n  value = \"v\"\n}\n", "examples/e/main.tf": "module \"this\" {\n  source = \"../../\"\n}\noutput \"x\" {\n  value = module.this.old\n}\n"}
	head := maps.Clone(base)
	head["outputs.tf"] = "output \"other\" {\n  value = \"w\"\n}\n\noutput \"new\" {\n  value = \"v\"\n}\n"
	cs := candidatesOf(runBase(t, base, head), staleRule)
	if len(cs) != 1 || cs[0].Line == nil || *cs[0].Line != 5 || cs[0].Subject != ".:output.old" || *cs[0].Evidence.Head != "output.new" {
		t.Fatalf("candidates %+v", cs)
	}
}

func TestStaleReferenceNoBase(t *testing.T) {
	if s := statusOf(runHead(t, map[string]string{"main.tf": "", "examples/e/main.tf": ""}).Manifest, staleRule, "."); s != "not_evaluated: no_base" {
		t.Error(s)
	}
}

func TestStaleReferenceEvidenceCap(t *testing.T) {
	var reads strings.Builder
	for i := range maxEvidenceComments + 1 {
		fmt.Fprintf(&reads, "output \"o%d\" {\n  value = module.this.gone\n}\n", i)
	}
	base := map[string]string{"outputs.tf": "output \"gone\" {\n  value = \"g\"\n}\n", "examples/e/main.tf": "module \"this\" {\n  source = \"../../\"\n}\n", "examples/e/o.tf": reads.String()}
	head := maps.Clone(base)
	head["outputs.tf"] = ""
	cs := candidatesOf(runBase(t, base, head), staleRule)
	if len(cs) != 1 || cs[0].EvidenceComplete || len(cs[0].Evidence.SourceLines) != 1+maxEvidenceComments || cs[0].Evidence.SourceLines[0].Revision != "merge_base" {
		t.Fatalf("candidates %+v", cs)
	}
}
