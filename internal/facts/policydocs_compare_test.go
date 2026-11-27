package facts

import "testing"

// Survivors the last mutation run showed once fewer mutants timed out.
func TestPolicyDocsCompareGuards(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
	const xAddr = "data.aws_iam_policy_document.x"
	run := func(t *testing.T, head, base map[string]string) *PolicyDocsReport {
		t.Helper()
		o := PolicyDocsOptions{Head: writeTree(t, head), Limits: DefaultLimits}
		if base != nil {
			o.Base = writeTree(t, base)
		}
		r, err := PolicyDocs(o)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("an undecided head beside a decided base is undecidable", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "data \"aws_iam_policy_document\" \"x\" {\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [\"${module.m.arn}\"]\n  }\n}\nmodule \"m\" {\n  source = \"./m\"\n}\n", "m/main.tf": "output \"arn\" {\n  value = \"a\"\n}\n"},
			map[string]string{"main.tf": doc + "module \"m\" {\n  source = \"./m\"\n}\n", "m/main.tf": "output \"arn\" {\n  value = \"a\"\n}\n"})
		if p := policyPair(t, r, ".", xAddr); p.Content == nil || *p.Content != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("a duplicate at the base alone", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc}, map[string]string{"main.tf": doc, "b.tf": doc})
		if p := policyPair(t, r, ".", xAddr); p.Presence != PresenceUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("a splat of another attribute into a merge list", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc + "data \"aws_iam_policy_document\" \"m\" {\n  source_policy_documents = data.aws_iam_policy_document.x[*].id\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.m.json\n}\n"}, nil)
		if d := policyDoc(t, r.Head, ".", xAddr); d.Uses != nil || d.UsesUndecidable == nil {
			t.Errorf("uses: %s", canonJSON(d.Uses))
		}
	})
	t.Run("another encoding of a policy object is no JSON document", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "resource \"aws_iam_policy\" \"p\" {\n  policy = yamlencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"*\", Resource = \"*\"}]})\n}\n"}, nil)
		if d := policyDoc(t, r.Head, ".", "aws_iam_policy.p.policy"); d.Kind != PolicyKindExpression {
			t.Errorf("document: %+v", d)
		}
	})
}
