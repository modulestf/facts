variable "create" { default = true }
data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  count  = var.create ? 1 : 0
  policy = data.aws_iam_policy_document.child.json
}
