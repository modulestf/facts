data "aws_iam_policy_document" "child" {
  statement {
    effect    = "Deny"
    actions   = ["iam:*"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
  }
}
output "json" {
  value = data.aws_iam_policy_document.child.json
}
