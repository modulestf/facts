data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child.json
}
