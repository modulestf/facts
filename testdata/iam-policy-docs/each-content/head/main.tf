data "aws_iam_policy_document" "child" {
  for_each = toset(["*"])
  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${each.key}/*"]
  }
}
