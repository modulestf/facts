package facts

import "testing"

// A dynamic block whose iterator is a quoted string, which Terraform
// rejects: the block's for_each is not read, so the key must be undecided,
// never equal while the for_each changes.
func TestPolicyDocsQuotedIterator(t *testing.T) {
	tree := func(def string) map[string]string {
		return map[string]string{"main.tf": `variable "permissions" {
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
    iterator = "it"
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
	r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, tree(`{a = {}}`)), Base: writeTree(t, tree(`{}`)), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Uses == nil || *p.Uses != CompareUndecidable {
		t.Errorf("pair: %+v", p)
	}
}
