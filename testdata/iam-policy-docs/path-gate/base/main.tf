locals {
  create = path.module == "."
}
data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  count  = local.create ? 1 : 0
  policy = data.aws_iam_policy_document.child.json
}
