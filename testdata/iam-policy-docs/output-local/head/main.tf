data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
locals {
  b = ["{}"]
  a = concat([data.aws_iam_policy_document.child.json], local.b)
}
output "all" {
  value = local.a
}
