locals {
  settings = yamldecode(file("${path.module}/settings.yaml"))
}
data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  count  = local.settings.create ? 1 : 0
  policy = data.aws_iam_policy_document.child.json
}
