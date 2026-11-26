data "aws_iam_policy_document" "d" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "m" {
  count                   = 0
  source_policy_documents = [data.aws_iam_policy_document.d.json]
}
locals {
  a = data.aws_iam_policy_document.d.json
  z = [data.aws_iam_policy_document.m[0].json, local.a]
}
data "aws_iam_policy_document" "n" {
  source_policy_documents = local.z
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.n.json
}
