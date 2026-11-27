package facts

import "testing"

// A dynamic block whose iterator is not a bare name, which Terraform
// rejects: a key that reads the block must be undecided, never equal while
// the block's for_each changes, also beside a top-level block whose address
// a marker could share.
func TestPolicyDocsQuotedIterator(t *testing.T) {
	tree := func(iter, def, extra string) map[string]string {
		return map[string]string{"main.tf": extra + `locals {
  x = "it"
}
variable "permissions" {
  default = ` + def + `
}
data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "m" {
  source_policy_documents = [data.aws_iam_policy_document.x.json]
  dynamic "statement" {
    for_each = var.permissions
    iterator = ` + iter + `
    content {
      actions = ["s3:PutObject"]
    }
  }
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.m.json
}
`}
	}
	const collide = "iterator \"quoted\" {}\n"
	for _, iter := range []string{`"it"`, `local.x`, `it[0]`} {
		for name, extra := range map[string]string{"": "", " beside an iterator block": collide} {
			t.Run(iter+name, func(t *testing.T) {
				r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, tree(iter, `{a = {}}`, extra)), Base: writeTree(t, tree(iter, `{}`, extra)), Limits: DefaultLimits})
				if err != nil {
					t.Fatal(err)
				}
				if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Uses == nil || *p.Uses != CompareUndecidable {
					t.Errorf("pair: %+v", p)
				}
			})
		}
	}
	// The refusal holds at depth: a quoted iterator on a dynamic condition
	// inside a bare-named dynamic statement.
	t.Run("a nested quoted iterator", func(t *testing.T) {
		nested := func(def string) map[string]string {
			return map[string]string{"main.tf": collide + `variable "permissions" {
  default = ` + def + `
}
data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "m" {
  source_policy_documents = [data.aws_iam_policy_document.x.json]
  dynamic "statement" {
    for_each = ["one"]
    iterator = st
    content {
      actions = ["s3:PutObject"]
      dynamic "condition" {
        for_each = var.permissions
        iterator = "it"
        content {
          test     = "StringEquals"
          variable = "aws:SourceAccount"
          values   = ["1"]
        }
      }
    }
  }
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.m.json
}
`}
		}
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, nested(`{a = {}}`)), Base: writeTree(t, nested(`{}`)), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Uses == nil || *p.Uses != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("a bare name iterator stays decided", func(t *testing.T) {
		files := tree("perm", `{}`, collide)
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, files), Base: writeTree(t, files), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Uses == nil || *p.Uses != CompareEqual {
			t.Errorf("pair: %+v", p)
		}
	})
}
