data "aws_iam_policy_document" "widget" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "admin" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
locals {
  extra = []
  docs  = concat(local.extra, [data.aws_iam_policy_document.widget.json])
}
data "aws_iam_policy_document" "merged" {
  source_policy_documents = local.docs
}
resource "aws_iam_role_policy" "this" {
  role   = "r"
  policy = data.aws_iam_policy_document.merged.json
}
