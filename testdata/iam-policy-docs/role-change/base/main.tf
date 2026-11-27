data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_role_policy" "this" {
  role   = "reader"
  policy = data.aws_iam_policy_document.child.json
}
