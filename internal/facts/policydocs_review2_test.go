package facts

import "testing"

// The false equals a second review reproduced, each held here: the pair
// must not compare equal.
func TestPolicyDocsSecondReview(t *testing.T) {
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
	notEqual := func(t *testing.T, p PolicyPair, field string) {
		t.Helper()
		v := p.Content
		if field == "uses" {
			v = p.Uses
		}
		if p.Presence != PresenceBoth || v == nil || *v == CompareEqual {
			t.Errorf("%s: %+v", field, p)
		}
	}
	const xAddr = "data.aws_iam_policy_document.x"
	docReading := func(resource, extra string) string {
		return extra + "data \"aws_iam_policy_document\" \"x\" {\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [" + resource + "]\n  }\n}\n"
	}
	// F-001: refAddress names an ephemeral value by its type alone.
	t.Run("an ephemeral parameter whose arn changes", func(t *testing.T) {
		eph := func(arn string) string {
			return "ephemeral \"aws_ssm_parameter\" \"target\" {\n  arn = \"" + arn + "\"\n}\n"
		}
		r := run(t, map[string]string{"main.tf": docReading("ephemeral.aws_ssm_parameter.target.value", eph("arn:aws:ssm:us-east-1:111111111111:parameter/b"))},
			map[string]string{"main.tf": docReading("ephemeral.aws_ssm_parameter.target.value", eph("arn:aws:ssm:us-east-1:111111111111:parameter/a"))})
		notEqual(t, policyPair(t, r, ".", xAddr), "content")
	})
	t.Run("an ephemeral random password whose length changes", func(t *testing.T) {
		eph := func(n string) string { return "ephemeral \"random_password\" \"p\" {\n  length = " + n + "\n}\n" }
		r := run(t, map[string]string{"main.tf": docReading("\"arn:aws:s3:::${ephemeral.random_password.p.result}\"", eph("9"))},
			map[string]string{"main.tf": docReading("\"arn:aws:s3:::${ephemeral.random_password.p.result}\"", eph("8"))})
		notEqual(t, policyPair(t, r, ".", xAddr), "content")
	})
	// F-002: an inline document's sites, its local and an output use.
	const enc = "jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"s3:GetObject\", Resource = \"*\"}]})"
	const extra = "variable \"extra\" {\n  default = \"\"\n}\n"
	t.Run("an output of an inline document gains a value", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": extra + "output \"policies\" {\n  value = [" + enc + ", var.extra]\n}\n"},
			map[string]string{"main.tf": extra + "output \"policies\" {\n  value = [" + enc + "]\n}\n"})
		notEqual(t, policyPair(t, r, ".", "output.policies.value"), "uses")
	})
	t.Run("a local an output reads gains a value beside an inline document", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": extra + "locals {\n  l = [" + enc + ", var.extra]\n}\noutput \"policies\" {\n  value = local.l\n}\n"},
			map[string]string{"main.tf": extra + "locals {\n  l = [" + enc + "]\n}\noutput \"policies\" {\n  value = local.l\n}\n"})
		notEqual(t, policyPair(t, r, ".", "local.l"), "uses")
	})
	t.Run("an output use gains a precondition", func(t *testing.T) {
		doc := docReading("\"*\"", "")
		r := run(t, map[string]string{"main.tf": doc + "output \"o\" {\n  value = data.aws_iam_policy_document.x.json\n  precondition {\n    condition     = false\n    error_message = \"no\"\n  }\n}\n"},
			map[string]string{"main.tf": doc + "output \"o\" {\n  value = data.aws_iam_policy_document.x.json\n}\n"})
		notEqual(t, policyPair(t, r, ".", xAddr), "uses")
	})
	// F-003: a reference to an iterator symbol names no definition.
	t.Run("a dynamic iterator in an inline document", func(t *testing.T) {
		role := func(acts string) string {
			return "resource \"aws_iam_role\" \"r\" {\n  name = \"r\"\n  dynamic \"inline_policy\" {\n    for_each = " + acts + "\n    content {\n      name   = \"p\"\n      policy = jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = inline_policy.value, Resource = \"*\"}]})\n    }\n  }\n}\n"
		}
		r := run(t, map[string]string{"main.tf": role("[\"*\"]")}, map[string]string{"main.tf": role("[\"s3:GetObject\"]")})
		p := policyPair(t, r, ".", "aws_iam_role.r.inline_policy[dynamic].policy")
		if p.Content == nil || *p.Content == CompareEqual {
			t.Errorf("content: %+v", p)
		}
	})
	forDoc := func(def string) string {
		return "variable \"acts\" {\n  default = " + def + "\n}\nresource \"aws_iam_policy\" \"p\" {\n  for_each = toset([for a in var.acts : jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = [a], Resource = \"*\"}]})])\n  policy   = each.value\n}\n"
	}
	t.Run("a for expression variable in an inline document", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": forDoc("[\"*\"]")}, map[string]string{"main.tf": forDoc("[\"s3:GetObject\"]")})
		p := policyPair(t, r, ".", "aws_iam_policy.p.for_each")
		if p.Content == nil || *p.Content == CompareEqual {
			t.Errorf("content: %+v", p)
		}
	})
	t.Run("a for expression over a map in an output", func(t *testing.T) {
		out := func(def string) string {
			return "variable \"m\" {\n  default = " + def + "\n}\noutput \"o\" {\n  value = {for k, v in var.m : k => jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = v, Resource = \"*\"}]})}\n}\n"
		}
		r := run(t, map[string]string{"main.tf": out("{a = \"*\"}")}, map[string]string{"main.tf": out("{a = \"s3:GetObject\"}")})
		p := policyPair(t, r, ".", "output.o.value")
		if p.Content == nil || *p.Content == CompareEqual {
			t.Errorf("content: %+v", p)
		}
	})
	t.Run("a merging document's own dynamic iterator keeps its uses decided", func(t *testing.T) {
		files := map[string]string{"main.tf": docReading("\"*\"", "") + `variable "permissions" {
  default = {}
}
data "aws_iam_policy_document" "m" {
  source_policy_documents = [data.aws_iam_policy_document.x.json]
  dynamic "statement" {
    for_each = var.permissions
    iterator = perm
    content {
      actions = perm.value.actions
    }
  }
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.m.json
}
`}
		if p := policyPair(t, run(t, files, files), ".", xAddr); p.Uses == nil || *p.Uses != CompareEqual {
			t.Errorf("uses: %+v", p)
		}
	})
	// F-004: a tree whose only directory is undecidable has no pair.
	t.Run("the only directory is undecidable", func(t *testing.T) {
		files := map[string]string{"main.tf": docReading("\"*\"", ""), "main_override.tf": "locals {}\n"}
		r := run(t, files, files)
		if r.Precondition == nil || *r.Precondition {
			t.Errorf("precondition: %v", r.Precondition)
		}
		ok := run(t, map[string]string{"main.tf": docReading("\"*\"", "")}, map[string]string{"main.tf": docReading("\"*\"", "")})
		if ok.Precondition == nil || !*ok.Precondition {
			t.Errorf("precondition of clean trees: %v", ok.Precondition)
		}
		if head := run(t, files, nil); head.Precondition != nil {
			t.Errorf("precondition without a base: %v", *head.Precondition)
		}
	})
	// F-005: a JSON Principal {"*": "*"} is not the bare "*".
	t.Run("a principal object keyed by a star", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "resource \"aws_s3_bucket_policy\" \"b\" {\n  bucket = \"b\"\n  policy = jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"s3:GetObject\", Resource = \"*\", Principal = {\"*\" = \"*\"}}]})\n}\nresource \"aws_s3_bucket_policy\" \"c\" {\n  bucket = \"c\"\n  policy = jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"s3:GetObject\", Resource = \"*\", Principal = \"*\"}]})\n}\n"}, nil)
		if d := policyDoc(t, r.Head, ".", "aws_s3_bucket_policy.b.policy"); d.Content != nil {
			t.Errorf("{\"*\": \"*\"} read: %s", canonJSON(d.Content))
		}
		if d := policyDoc(t, r.Head, ".", "aws_s3_bucket_policy.c.policy"); d.Content == nil {
			t.Errorf("\"*\" not read: %+v", d.ContentUndecidable)
		}
	})
}
