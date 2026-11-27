data "aws_iam_policy_document" "gone" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
