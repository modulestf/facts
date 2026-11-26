package facts

import "testing"

// The shapes that gave a false "equal" in reviews of a host's earlier
// line-based reader, each held here against this reader: a pair that differs
// for Terraform must not compare equal.
func TestPolicyDocsReviewShapes(t *testing.T) {
	stmt := func(body string) string {
		return "data \"aws_iam_policy_document\" \"x\" {\n  statement {\n" + body + "  }\n}\n" +
			"resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json\n}\n"
	}
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
	x := func(t *testing.T, r *PolicyDocsReport) PolicyPair {
		t.Helper()
		return policyPair(t, r, ".", "data.aws_iam_policy_document.x")
	}
	differs := map[string][2]string{
		"an escaped interpolation is literal text": {
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::b/$${var.u}\"]\n"),
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::b/${var.u}\"]\n"),
		},
		"an escaped directive is literal text": {
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::b/%%{ if true }x%%{ endif }\"]\n"),
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::b/%{ if true }x%{ endif }\"]\n"),
		},
		"white space inside a string": {
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::x/a b\"]\n"),
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::x/ab\"]\n"),
		},
		"not_actions against actions": {
			stmt("    not_actions = [\"s3:GetObject\"]\n    resources   = [\"*\"]\n"),
			stmt("    actions     = [\"s3:GetObject\"]\n    resources   = [\"*\"]\n"),
		},
		"Deny against Allow": {
			stmt("    effect    = \"Deny\"\n    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n"),
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n"),
		},
		"one value holding a separator against two values": {
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"a,b\"]\n"),
			stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"a\", \"b\"]\n"),
		},
		"a condition value against a principal": {
			stmt("    actions   = [\"sts:AssumeRole\"]\n    principals {\n      type        = \"AWS\"\n      identifiers = [\"arn:aws:iam::111111111111:root\"]\n    }\n"),
			stmt("    actions   = [\"sts:AssumeRole\"]\n    condition {\n      test     = \"StringEquals\"\n      variable = \"AWS\"\n      values   = [\"arn:aws:iam::111111111111:root\"]\n    }\n"),
		},
	}
	for name, c := range differs {
		t.Run(name, func(t *testing.T) {
			v := "variable \"u\" {\n  type = string\n}\n"
			p := x(t, run(t, map[string]string{"main.tf": c[0] + v}, map[string]string{"main.tf": c[1] + v}))
			// A real directive makes the base content undecidable; never equal.
			want := CompareDiffers
			if name == "an escaped directive is literal text" {
				want = CompareUndecidable
			}
			if p.Presence != PresenceBoth || p.Content == nil || *p.Content != want {
				t.Errorf("pair: %+v", p)
			}
		})
	}
	t.Run("a merging document with a statement of its own", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n") + `data "aws_iam_policy_document" "m" {
  source_policy_documents = [data.aws_iam_policy_document.x.json]
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
resource "aws_iam_role_policy" "r" {
  role   = "r"
  policy = data.aws_iam_policy_document.m.json
}
`}, nil)
		if m := policyDoc(t, r.Head, ".", "data.aws_iam_policy_document.m"); m.Content != nil || reasonCode(m.ContentUndecidable) != PolicyReasonMerge {
			t.Errorf("merging document: %+v", m.ContentUndecidable)
		}
	})
	t.Run("a merge list behind a conditional", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": stmt("    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n") + `data "aws_iam_policy_document" "wide" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "m" {
  source_policy_documents = false ? [data.aws_iam_policy_document.x.json] : [data.aws_iam_policy_document.wide.json]
}
resource "aws_iam_role_policy" "r" {
  role   = "r"
  policy = data.aws_iam_policy_document.m.json
}
`}, nil)
		for _, a := range []string{"data.aws_iam_policy_document.x", "data.aws_iam_policy_document.wide"} {
			if d := policyDoc(t, r.Head, ".", a); d.Uses != nil || reasonCode(d.UsesUndecidable) != PolicyReasonConditional {
				t.Errorf("%s uses: %+v %+v", a, d.Uses, d.UsesUndecidable)
			}
		}
	})
	t.Run("a dot file holding the only use", func(t *testing.T) {
		doc := "data \"aws_iam_policy_document\" \"x\" {\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n  }\n}\n"
		use := "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json\n}\n"
		p := x(t, run(t, map[string]string{"main.tf": doc, ".x.tf": use}, map[string]string{"main.tf": doc + use}))
		if p.Uses == nil || *p.Uses != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
}
