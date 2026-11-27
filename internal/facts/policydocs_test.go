package facts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const policyCases = "../../testdata/iam-policy-docs"

// runPolicyCase runs the case in dir: head, and base when the case has one.
func runPolicyCase(t *testing.T, dir string) *PolicyDocsReport {
	t.Helper()
	o := PolicyDocsOptions{Head: filepath.Join(dir, "head"), Limits: DefaultLimits}
	if _, err := os.Stat(filepath.Join(dir, "base")); err == nil {
		o.Base = filepath.Join(dir, "base")
	}
	r, err := PolicyDocs(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func policyJSON(t *testing.T, r *PolicyDocsReport) []byte {
	t.Helper()
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

// TestPolicyDocsGolden holds every case to its expected.json, and every
// expected.json to the schema.
func TestPolicyDocsGolden(t *testing.T) {
	cases, err := filepath.Glob(filepath.Join(policyCases, "*", "head"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("no cases: %v", err)
	}
	for _, h := range cases {
		dir := filepath.Dir(h)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			got := policyJSON(t, runPolicyCase(t, dir))
			want := filepath.Join(dir, "expected.json")
			if *update {
				if err := os.WriteFile(want, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			exp, err := os.ReadFile(want)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, exp) {
				t.Errorf("output differs from %s; run go test -run TestPolicyDocsGolden -update and review the diff\n%s", want, got)
			}
			validateAgainst(t, "iam-policy-docs.schema.json", exp)
		})
		t.Run(filepath.Base(dir)+"/unique addresses", func(t *testing.T) {
			r := runPolicyCase(t, dir)
			for _, side := range []*PolicyReport{r.Head, r.Base} {
				if side == nil {
					continue
				}
				seen := map[string]bool{}
				for _, d := range side.Documents {
					k := d.Dir + " " + d.Address
					if seen[k] && d.ContentUndecidable.Code != PolicyReasonDuplicate {
						t.Errorf("%s listed twice", k)
					}
					seen[k] = true
				}
			}
		})
	}
}

// policyDoc returns the document of a tree report by directory and address.
func policyDoc(t *testing.T, r *PolicyReport, dir, addr string) PolicyDoc {
	t.Helper()
	for _, d := range r.Documents {
		if d.Dir == dir && d.Address == addr {
			return d
		}
	}
	t.Fatalf("no document %s %s", dir, addr)
	return PolicyDoc{}
}

func policyPair(t *testing.T, r *PolicyDocsReport, dir, addr string) PolicyPair {
	t.Helper()
	for _, p := range r.Compare {
		if p.Dir == dir && p.Address == addr {
			return p
		}
	}
	t.Fatalf("no pair %s %s", dir, addr)
	return PolicyPair{}
}

func values(vs []PolicyValue) string { return canonJSON(vs) }

// The facts each historical false "matches upstream" turned on, stated
// apart from the golden files so a careless -update cannot hide them.
func TestPolicyDocsFailClosed(t *testing.T) {
	t.Run("merge through a flatten(concat()) local", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "merge-local"))
		d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.load_balancer_controller")
		if d.Content == nil || len(d.Content.Statements) != 2 {
			t.Fatalf("content: %+v %+v", d.Content, d.ContentUndecidable)
		}
		if got := values(d.Content.Statements[1].Resources); got != `[{"template":["arn:",{"ref":"local.partition"},":ec2:*:*:security-group/*"]}]` {
			t.Errorf("template: %s", got)
		}
		if got := values(d.Content.Statements[0].Conditions[0].Values); got != `[{"template":[{"ref":"data.aws_service_principal.elasticloadbalancing[0].name"}]}]` {
			t.Errorf("reference: %s", got)
		}
		if len(d.Uses) != 1 || d.Uses[0].Address != "aws_iam_policy.this" || d.Uses[0].Argument != "policy" {
			t.Fatalf("uses: %+v %+v", d.Uses, d.UsesUndecidable)
		}
		if got := canonJSON(d.Uses[0].Via); !strings.Contains(got, `"node":"local.source_policy_documents","role":"local"`) ||
			!strings.Contains(got, `"node":"data.aws_iam_policy_document.this","role":"source"`) {
			t.Errorf("via: %s", got)
		}
		if len(d.Uses[0].Gates) != 3 || d.Uses[0].Instances != InstancesConditional {
			t.Errorf("gates: %+v", d.Uses[0].Gates)
		}
		m := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.this")
		if m.Content != nil || m.ContentUndecidable.Code != PolicyReasonMerge {
			t.Errorf("a merging document is undecidable, got %+v", m.ContentUndecidable)
		}
		p := policyPair(t, r, ".", "data.aws_iam_policy_document.load_balancer_controller")
		if *p.Content != CompareDiffers || *p.Uses != CompareEqual {
			t.Errorf("compare: %s %s", *p.Content, *p.Uses)
		}
	})
	t.Run("a comment inside an interpolation", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "comment-in-interpolation"))
		d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.widget")
		if d.Content == nil || values(d.Content.Statements[0].Actions) != `["*"]` {
			t.Fatalf("the document Terraform reads grants *: %+v", d.Content)
		}
		if n := strings.Count(string(policyJSON(t, r)), `"address": "data.aws_iam_policy_document.widget"`); n != 1 {
			t.Errorf("the copy inside the comment is a document: %d", n)
		}
	})
	t.Run("quotes and markers in comments", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "quotes-in-comments"))
		if len(r.Head.Documents) != 1 {
			t.Fatalf("documents: %d", len(r.Head.Documents))
		}
		d := r.Head.Documents[0]
		if got := values(d.Content.Statements[0].Resources); got != `["arn:aws:s3:::b/*","arn:aws:s3:::bucket/${aws:username}/*"]` {
			t.Errorf("escaped interpolation: %s", got)
		}
	})
	t.Run("a hidden file", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "hidden-file"))
		d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.widget")
		if d.Uses == nil || len(d.Uses) != 0 {
			t.Errorf("a use in a file Terraform ignores counts: %+v", d.Uses)
		}
		a := policyDoc(t, r.Head, ".", "aws_iam_policy.p.policy")
		if a.Kind != PolicyKindJSONEncode || values(a.Content.Statements[0].Actions) != `["*"]` {
			t.Errorf("inline admin policy: %+v", a)
		}
	})
	t.Run("an admin document through the local of the merge list", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "concat-local"))
		p := policyPair(t, r, ".", "data.aws_iam_policy_document.widget")
		if *p.Content != CompareEqual || *p.Uses != CompareDiffers {
			t.Errorf("compare: %s %s", *p.Content, *p.Uses)
		}
		a := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.admin")
		if len(a.Uses) != 1 || a.Uses[0].Address != "aws_iam_role_policy.this" {
			t.Errorf("admin uses: %+v %+v", a.Uses, a.UsesUndecidable)
		}
	})
	t.Run("a new document with no base", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "new-document"))
		p := policyPair(t, r, ".", "data.aws_iam_policy_document.widget")
		if p.Presence != PresenceHeadOnly || p.Content != nil || p.Uses != nil {
			t.Errorf("pair: %+v", p)
		}
		d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.widget")
		if len(d.Uses) != 1 || d.Uses[0].Instances != InstancesNone {
			t.Errorf("a count = 0 use: %+v", d.Uses)
		}
		a := policyDoc(t, r.Head, ".", "aws_iam_policy.admin.policy")
		if a.Kind != PolicyKindJSONString || a.Content == nil {
			t.Errorf("heredoc admin policy: %+v", a)
		}
		s := policyDoc(t, r.Base, ".", "aws_iam_role.this.assume_role_policy")
		if s.Kind != PolicyKindExpression || s.ContentUndecidable.Code != PolicyReasonUnresolved {
			t.Errorf("a policy argument no document explains: %+v", s)
		}
	})
	t.Run("undecidable directories", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "undecidable"))
		for _, d := range r.Head.Directories {
			if d.Undecidable == nil {
				t.Errorf("%s is decided", d.Dir)
			}
		}
		for _, d := range r.Head.Documents {
			if d.Content != nil || d.Uses != nil || d.ContentUndecidable.Code != PolicyReasonDirectory {
				t.Errorf("%s %s: %+v", d.Dir, d.Address, d)
			}
		}
		if p := policyPair(t, r, "a", "data.aws_iam_policy_document.gone"); p.Presence != PresenceUndecidable {
			t.Errorf("a base document missing from an undecidable head directory is %s", p.Presence)
		}
		if p := policyPair(t, r, "c", "data.aws_iam_policy_document.widget"); p.Presence != PresenceUndecidable {
			t.Errorf("a directory the base lacks, in a tree that fails the precondition: %s", p.Presence)
		}
	})
	t.Run("module inputs and outputs", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "modules"))
		d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.root")
		if len(d.Uses) != 1 || d.Uses[0].Dir != "modules/role" || d.Uses[0].Address != "aws_iam_role_policy.this" {
			t.Errorf("through a module input: %+v %+v", d.Uses, d.UsesUndecidable)
		}
		c := policyDoc(t, r.Head, "modules/docs", "data.aws_iam_policy_document.child")
		if len(c.Uses) != 2 || c.Uses[0].Dir != "." || c.Uses[0].Address != "aws_iam_role_policy.from_child" || c.Uses[1].Address != "output.json" {
			t.Errorf("through a module output: %+v %+v", c.Uses, c.UsesUndecidable)
		}
		x := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.remote")
		if x.Uses != nil || x.UsesUndecidable.Code != PolicyReasonExternalModule {
			t.Errorf("an external module: %+v", x.UsesUndecidable)
		}
	})
	t.Run("a duplicate block", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "duplicate"))
		for _, d := range r.Head.Documents {
			if d.Content != nil || d.ContentUndecidable.Code != PolicyReasonDuplicate {
				t.Errorf("%s %s: %+v", d.File, d.Address, d.ContentUndecidable)
			}
		}
	})
	t.Run("callers that cannot be read", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "callers"))
		d := policyDoc(t, r.Head, "modules/docs", "data.aws_iam_policy_document.child")
		if d.Uses != nil || d.UsesUndecidable.Code != PolicyReasonCallerUnknown {
			t.Errorf("an output an undecidable directory may read: %+v", d.UsesUndecidable)
		}
	})
	t.Run("refused shapes", func(t *testing.T) {
		r := runPolicyCase(t, filepath.Join(policyCases, "refused"))
		want := map[string][2]string{ // address -> content reason, uses reason
			"data.aws_iam_policy_document.transformed":     {"", PolicyReasonFunction},
			"data.aws_iam_policy_document.conditional":     {"", PolicyReasonConditional},
			"data.aws_iam_policy_document.dynamic":         {PolicyReasonDynamicBlock, ""},
			"data.aws_iam_policy_document.list_from_local": {PolicyReasonNotLiteral, ""},
			"aws_iam_policy.d.policy":                      {PolicyReasonInterpolatedJSON, ""},
			"aws_iam_policy.e.policy":                      {PolicyReasonUnresolved, ""},
			"aws_iam_policy.b.policy":                      {PolicyReasonUnresolved, ""},
			"aws_iam_policy.a.policy":                      {PolicyReasonUnresolved, ""},
		}
		for addr, w := range want {
			d := policyDoc(t, r.Head, ".", addr)
			if got := reasonCode(d.ContentUndecidable); got != w[0] {
				t.Errorf("%s content: %q, want %q", addr, got, w[0])
			}
			if got := reasonCode(d.UsesUndecidable); got != w[1] {
				t.Errorf("%s uses: %q, want %q", addr, got, w[1])
			}
		}
		c := policyDoc(t, r.Head, ".", "aws_iam_policy.c.policy")
		if c.Content == nil || values(c.Content.Statements[0].Conditions[0].Values) != `["true"]` ||
			!slices.ContainsFunc(c.Content.Statements[0].Resources, func(v PolicyValue) bool { return v.Template != nil }) {
			t.Errorf("jsonencode with a reference: %+v", c.Content)
		}
	})
}

func reasonCode(r *PolicyReason) string {
	if r == nil {
		return ""
	}
	return r.Code
}

// Every reason code the report can write is in the schema's enum, and the
// other way round.
func TestPolicyReasonCodesInSchema(t *testing.T) {
	data, err := os.ReadFile("../../schema/iam-policy-docs.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Defs struct {
			Reason struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"reason"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("policydocs.go")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for line := range strings.Lines(string(src)) {
		if f := strings.Fields(line); len(f) >= 3 && strings.HasPrefix(f[0], "PolicyReason") && f[1] == "=" {
			codes = append(codes, strings.Trim(f[2], `"`))
		}
	}
	codes = append(codes, ReasonParseError, ReasonUnsupportedFile, ReasonSymlink, ReasonLimitExceeded, ReasonNoBase)
	slices.Sort(codes)
	enum := slices.Sorted(slices.Values(s.Defs.Reason.Properties.Code.Enum))
	if !slices.Equal(codes, enum) {
		t.Errorf("reason codes %v, schema enum %v", codes, enum)
	}
}

// A tree over a limit, and the deadline, are errors, not a short report.
func TestPolicyDocsLimits(t *testing.T) {
	lim := DefaultLimits
	lim.MaxFiles = 1
	if _, err := PolicyDocs(PolicyDocsOptions{Head: filepath.Join(policyCases, "modules", "head"), Limits: lim}); err == nil {
		t.Error("a tree over the file limit gave a report")
	}
	lim = DefaultLimits
	lim.Deadline = lim.Deadline.AddDate(1, 0, 0) // the zero time plus a year: long past
	if _, err := PolicyDocs(PolicyDocsOptions{Head: filepath.Join(policyCases, "modules", "head"), Limits: lim}); err == nil {
		t.Error("a passed deadline gave a report")
	}
	head := writeTree(t, map[string]string{"main.tf": "locals { x = " + strings.Repeat("[", DefaultLimits.MaxDepth+1) + strings.Repeat("]", DefaultLimits.MaxDepth+1) + " }\n"})
	r, err := PolicyDocs(PolicyDocsOptions{Head: head, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if r.Head.Directories[0].Undecidable == nil || r.Head.Directories[0].Undecidable.Code != ReasonLimitExceeded {
		t.Errorf("a file over the nesting limit: %+v", r.Head.Directories[0])
	}
}

// validateAgainst checks data against a schema of schema/ with the
// validator lint's reports are checked with.
func validateAgainst(t *testing.T, id string, data []byte) {
	t.Helper()
	if errs := loadSchemas(t).Validate(id, decodeJSON(t, data)); len(errs) > 0 {
		t.Errorf("%s: %v", id, errs)
	}
}

// The schema examples hold in the validator lint's reports are checked
// with: each valid one passes and each invalid one fails.
func TestPolicyDocsSchemaExamples(t *testing.T) {
	set := loadSchemas(t)
	files, err := filepath.Glob("../../schema/examples/iam-policy-docs.*.json")
	if err != nil || len(files) < 3 {
		t.Fatalf("examples: %v %v", files, err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: an example of this repository
		if err != nil {
			t.Fatal(err)
		}
		errs := set.Validate("iam-policy-docs.schema.json", decodeJSON(t, data))
		if strings.Contains(f, ".invalid.") == (len(errs) == 0) {
			t.Errorf("%s: %v", filepath.Base(f), errs)
		}
	}
}

// Uses the reader refuses, each in a tree of its own.
func TestPolicyDocsRefusedUses(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
	for name, c := range map[string]struct {
		files map[string]string
		code  string
	}{
		"a dynamic block": {map[string]string{"main.tf": doc + `resource "aws_iam_role" "r" {
  dynamic "inline_policy" {
    for_each = [1]
    content {
      policy = data.aws_iam_policy_document.x.json
    }
  }
}
`}, PolicyReasonDynamicBlock},
		"a merge list that is a string": {map[string]string{"main.tf": doc + `data "aws_iam_policy_document" "m" {
  source_policy_documents = data.aws_iam_policy_document.x.json
}
`}, PolicyReasonShape},
		"a policy that is a list": {map[string]string{"main.tf": doc + `resource "aws_iam_policy" "p" {
  policy = [data.aws_iam_policy_document.x.json]
}
`}, PolicyReasonShape},
		"the whole module object": {map[string]string{
			"main.tf":   "module \"m\" {\n  source = \"./m\"\n}\noutput \"all\" {\n  value = module.m\n}\n",
			"m/main.tf": doc + "output \"json\" {\n  value = data.aws_iam_policy_document.x.json\n}\n",
		}, PolicyReasonIndex},
		"an index into a local": {map[string]string{"main.tf": doc + `locals {
  docs = [data.aws_iam_policy_document.x.json]
}
resource "aws_iam_policy" "p" {
  policy = local.docs[0]
}
`}, PolicyReasonIndex},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, c.files), Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			var d PolicyDoc
			for _, x := range r.Head.Documents {
				if x.Address == "data.aws_iam_policy_document.x" {
					d = x
				}
			}
			if d.Uses != nil || reasonCode(d.UsesUndecidable) != c.code {
				t.Errorf("uses %+v, reason %+v, want %s", d.Uses, d.UsesUndecidable, c.code)
			}
		})
	}
}

// Fix round 1: values from callers, unseen callers, paths with other gates,
// variable defaults and the wider sinks, each stated apart from the goldens.
func TestPolicyDocsCallersAndPaths(t *testing.T) {
	pair := func(c, dir, addr string) PolicyPair {
		return policyPair(t, runPolicyCase(t, filepath.Join(policyCases, c)), dir, addr)
	}
	const child = "data.aws_iam_policy_document.child"
	for c, want := range map[string]string{"caller-arg": CompareDiffers, "caller-count": CompareDiffers} {
		if p := pair(c, "modules/role", child); *p.Uses != want {
			t.Errorf("%s: uses %s, want %s", c, *p.Uses, want)
		}
	}
	if p := pair("tfvars", ".", child); p.Presence != PresenceUndecidable {
		t.Errorf("a variables file: presence %s", p.Presence)
	}
	for _, c := range []string{"caller-json", "caller-unparsed"} {
		d := policyDoc(t, runPolicyCase(t, filepath.Join(policyCases, c)).Head, "modules/docs", child)
		if reasonCode(d.UsesUndecidable) != PolicyReasonCallerUnknown {
			t.Errorf("%s: uses %+v %+v", c, d.Uses, d.UsesUndecidable)
		}
	}
	for _, c := range []string{"two-calls", "two-calls-reversed"} {
		d := policyDoc(t, runPolicyCase(t, filepath.Join(policyCases, c)).Head, ".", "data.aws_iam_policy_document.root")
		if len(d.Uses) != 2 || d.Uses[0].Instances == d.Uses[1].Instances {
			t.Errorf("%s: both calls are uses: %+v", c, d.Uses)
		}
	}
	for _, c := range []string{"merge-and-local", "merge-and-local-reversed"} {
		d := policyDoc(t, runPolicyCase(t, filepath.Join(policyCases, c)).Head, ".", "data.aws_iam_policy_document.d")
		if len(d.Uses) != 2 || !slices.ContainsFunc(d.Uses, func(u PolicyUse) bool { return u.Instances == InstancesAlways }) {
			t.Errorf("%s: the ungated path is lost: %+v", c, d.Uses)
		}
	}
	v := policyDoc(t, runPolicyCase(t, filepath.Join(policyCases, "variable-default")).Head, ".", "var.p.default")
	if reasonCode(v.UsesUndecidable) != PolicyReasonVariableDefault {
		t.Errorf("a variable default: %+v %+v", v.Uses, v.UsesUndecidable)
	}
	r := runPolicyCase(t, filepath.Join(policyCases, "sinks"))
	for _, addr := range []string{"provider.aws.assume_role[0].policy", "aws_opensearch_domain.d.access_policies",
		"aws_organizations_policy.o.content", "module.remote.policy"} {
		if d := policyDoc(t, r.Head, ".", addr); d.Kind != PolicyKindExpression {
			t.Errorf("%s: %s", addr, d.Kind)
		}
	}
	if p := pair("duplicate", ".", "data.aws_iam_policy_document.widget"); p.Presence != PresenceUndecidable {
		t.Errorf("a duplicate at head beside one at base: %s", p.Presence)
	}
}

// One case per guard of the keys and the trace: deleting the guard fails
// its case.
func TestPolicyDocsGuards(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
	same := func(t *testing.T, files map[string]string) *PolicyDocsReport {
		t.Helper()
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, files), Base: writeTree(t, files), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	x := func(t *testing.T, r *PolicyDocsReport) PolicyPair {
		t.Helper()
		return policyPair(t, r, ".", "data.aws_iam_policy_document.x")
	}
	t.Run("content reads a module output", func(t *testing.T) {
		r := same(t, map[string]string{"main.tf": `module "m" {
  source = "./m"
}
data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["${module.m.arn}/*"]
  }
}
`, "m/main.tf": "output \"arn\" {\n  value = \"a\"\n}\n"})
		if p := x(t, r); *p.Content != CompareUndecidable {
			t.Errorf("content %s", *p.Content)
		}
	})
	t.Run("content reads a block defined twice", func(t *testing.T) {
		r := same(t, map[string]string{"main.tf": `data "aws_partition" "p" {}
data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:${data.aws_partition.p.partition}:s3:::b"]
  }
}
`, "other.tf": "data \"aws_partition\" \"p\" {}\n"})
		if p := x(t, r); *p.Content != CompareUndecidable {
			t.Errorf("content %s", *p.Content)
		}
	})
	t.Run("a gate reads a module output", func(t *testing.T) {
		r := same(t, map[string]string{"main.tf": doc + `module "m" {
  source = "./m"
}
resource "aws_iam_policy" "p" {
  count  = module.m.n
  policy = data.aws_iam_policy_document.x.json
}
`, "m/main.tf": "output \"n\" {\n  value = 1\n}\n"})
		if p := x(t, r); *p.Uses != CompareUndecidable {
			t.Errorf("uses %s", *p.Uses)
		}
	})
	t.Run("a trace enters an undecidable directory", func(t *testing.T) {
		r := same(t, map[string]string{"main.tf": doc + `module "m" {
  source = "./m"
  policy = data.aws_iam_policy_document.x.json
}
`, "m/main.tf": "variable \"policy\" {}\n", "m/extra.tf.json": "{}\n"})
		if d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.x"); reasonCode(d.UsesUndecidable) != PolicyReasonDirectory {
			t.Errorf("uses %+v %+v", d.Uses, d.UsesUndecidable)
		}
	})
	two := map[string]string{"main.tf": doc + `resource "aws_iam_policy" "a" {
  policy = data.aws_iam_policy_document.x.json
}
resource "aws_iam_policy" "b" {
  policy = data.aws_iam_policy_document.x.json
}
`}
	t.Run("the step limit", func(t *testing.T) {
		defer func(v int) { maxPolicySteps = v }(maxPolicySteps)
		maxPolicySteps = 1
		if d := policyDoc(t, same(t, two).Head, ".", "data.aws_iam_policy_document.x"); reasonCode(d.UsesUndecidable) != PolicyReasonTooLarge {
			t.Errorf("uses %+v %+v", d.Uses, d.UsesUndecidable)
		}
	})
	t.Run("the use limit", func(t *testing.T) {
		defer func(v int) { maxPolicyUses = v }(maxPolicyUses)
		maxPolicyUses = 1
		if d := policyDoc(t, same(t, two).Head, ".", "data.aws_iam_policy_document.x"); reasonCode(d.UsesUndecidable) != PolicyReasonTooLarge {
			t.Errorf("uses %+v %+v", d.Uses, d.UsesUndecidable)
		}
	})
	t.Run("a sink reads a local that is not defined", func(t *testing.T) {
		r := same(t, map[string]string{"main.tf": "resource \"aws_iam_policy\" \"p\" {\n  policy = local.nope\n}\n"})
		if d := policyDoc(t, r.Head, ".", "aws_iam_policy.p.policy"); d.Kind != PolicyKindExpression {
			t.Errorf("kind %s", d.Kind)
		}
	})
}

// The contract of round 3: inputs Terraform reads that a closure cannot
// see make a comparison undecidable, and the guards of the call chain.
func TestPolicyDocsContract(t *testing.T) {
	u, d, e := CompareUndecidable, CompareDiffers, CompareEqual
	const pre = "precondition" // the tree fails the precondition: presence undecidable
	for c, w := range map[string]struct{ dir, addr, content, uses string }{
		"dot-tfvars":           {".", "child", pre, pre},
		"json-caller-gate":     {"modules/role", "child", pre, pre},
		"yaml-file-gate":       {".", "child", e, u},
		"templatefile-content": {".", "child", u, u},
		"tree-data":            {".", "child", u, u},
		"each-content":         {".", "child", d, d},
		"provider-change":      {".", "child", pre, pre},
		"lock-change":          {".", "child", pre, pre},
		"role-change":          {".", "child", e, d},
		"caller-gate-only":     {"modules/role", "child", e, d},
		"grandparent-count":    {"modules/mid/leaf", "child", e, d},
		"input-gate-local":     {".", "root", e, d},
		"dropped-argument":     {"modules/role", "child", e, d},
		"caller-gate-local":    {"modules/role", "child", e, d},
		"grandparent-local":    {"modules/mid/leaf", "child", e, d},
		"doc-gate-module":      {".", "child", e, u},
		"file-gate":            {".", "child", e, u},
		"path-gate":            {".", "child", e, u},
		"path-content":         {".", "child", u, u},
		"missing-source":       {".", "child", pre, pre},
		"output-local":         {".", "child", e, d},
		"each-module-content":  {".", "child", u, u},
	} {
		p := policyPair(t, runPolicyCase(t, filepath.Join(policyCases, c)), w.dir, "data.aws_iam_policy_document."+w.addr)
		if w.content == pre {
			if p.Presence != PresenceUndecidable {
				t.Errorf("%s: presence %s, want undecidable", c, p.Presence)
			}
			continue
		}
		if *p.Content != w.content || *p.Uses != w.uses {
			t.Errorf("%s: content %s uses %s, want %s %s", c, *p.Content, *p.Uses, w.content, w.uses)
		}
	}
	d0 := policyDoc(t, runPolicyCase(t, filepath.Join(policyCases, "hop-limit")).Head, ".", "data.aws_iam_policy_document.child")
	if reasonCode(d0.UsesUndecidable) != PolicyReasonTooLarge {
		t.Errorf("hop limit: %+v", d0.UsesUndecidable)
	}
	r := runPolicyCase(t, filepath.Join(policyCases, "caller-gate-only"))
	if i := policyDoc(t, r.Head, "modules/role", "data.aws_iam_policy_document.child").Uses[0].Instances; i != InstancesConditional {
		t.Errorf("a use under a gated call: %s", i)
	}
}

// A module directory reached through a link is outside what the reader
// can tie to the tree: every comparison is undecidable.
func TestPolicyDocsLinkedModule(t *testing.T) {
	const child = `data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child.json
}
`
	files := map[string]string{"main.tf": "module \"role\" {\n  source = \"./role\"\n}\n", "real/main.tf": child}
	base := writeTree(t, files)
	files["role"] = "->real"
	head := writeTree(t, files)
	r, err := PolicyDocs(PolicyDocsOptions{Head: head, Base: base, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range r.Compare {
		if p.Content != nil && (*p.Content != CompareUndecidable || *p.Uses != CompareUndecidable) {
			t.Errorf("%s %s: %s %s", p.Dir, p.Address, *p.Content, *p.Uses)
		}
	}
}

// Past the closure entry limit the run is an error, never a short report.
func TestPolicyDocsClosureLimit(t *testing.T) {
	defer func(v int) { maxPolicyClosure = v }(maxPolicyClosure)
	maxPolicyClosure = 1
	_, err := PolicyDocs(PolicyDocsOptions{Head: filepath.Join(policyCases, "merge-local", "head"), Limits: DefaultLimits})
	if !errors.Is(err, errPolicyClosure) {
		t.Errorf("err %v", err)
	}
}

// Round 4: holes inside the contract.
func TestPolicyDocsContractRound4(t *testing.T) {
	r := runPolicyCase(t, filepath.Join(policyCases, "provider-ref"))
	if p := policyPair(t, r, ".", "data.aws_iam_policy_document.child"); p.Presence != PresenceUndecidable {
		t.Errorf("a provider block that reads a changed local: %s", p.Presence)
	}
	r = runPolicyCase(t, filepath.Join(policyCases, "provider-file"))
	if p := policyPair(t, r, ".", "data.aws_iam_policy_document.child"); p.Presence != PresenceUndecidable {
		t.Errorf("a provider block that reads a file: %s", p.Presence)
	}
	r = runPolicyCase(t, filepath.Join(policyCases, "output-with-caller"))
	d := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.root")
	if len(d.Uses) != 2 || d.Uses[0].Address != "output.json" || d.Uses[1].Dir != "examples/complete" {
		t.Errorf("an output with an in-tree caller is a use too: %+v %+v", d.Uses, d.UsesUndecidable)
	}
	r = runPolicyCase(t, filepath.Join(policyCases, "legacy-merge"))
	d = policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.d")
	if len(d.Uses) != 1 || d.Uses[0].Address != "aws_iam_policy.this" || d.Uses[0].Via[0].Role != HopSource {
		t.Errorf("source_json is a merge hop: %+v %+v", d.Uses, d.UsesUndecidable)
	}
	for c, w := range map[string][2]string{"aws-file": {CompareUndecidable, CompareUndecidable}, "caller-file-arg": {CompareEqual, CompareUndecidable}} {
		dir := "."
		if c == "caller-file-arg" {
			dir = "modules/role"
		}
		p := policyPair(t, runPolicyCase(t, filepath.Join(policyCases, c)), dir, "data.aws_iam_policy_document.child")
		if *p.Content != w[0] || *p.Uses != w[1] {
			t.Errorf("%s: content %s uses %s, want %v", c, *p.Content, *p.Uses, w)
		}
	}
	r = runPolicyCase(t, filepath.Join(policyCases, "tfvars-presence"))
	if p := policyPair(t, r, ".", "data.aws_iam_policy_document.child"); p.Presence != PresenceUndecidable {
		t.Errorf("presence in a tree that fails the precondition: %s", p.Presence)
	}
}

// A lock file past the file size limit has no hash: equal lock text cannot
// be told, so the tree fails the precondition.
func TestPolicyDocsOversizeLock(t *testing.T) {
	const child = `data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child.json
}
`
	big := func(c string) string { return strings.Repeat(c, int(DefaultLimits.MaxFileBytes)+1) }
	base := writeTree(t, map[string]string{"main.tf": child, ".terraform.lock.hcl": big("a")})
	head := writeTree(t, map[string]string{"main.tf": child, ".terraform.lock.hcl": big("b")})
	r, err := PolicyDocs(PolicyDocsOptions{Head: head, Base: base, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if p := policyPair(t, r, ".", "data.aws_iam_policy_document.child"); p.Presence != PresenceUndecidable {
		t.Errorf("presence %s", p.Presence)
	}
}
