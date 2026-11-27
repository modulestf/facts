data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
output "json" {
  value = data.aws_iam_policy_document.child.json
}
