data "aws_iam_policy_document" "widget" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
