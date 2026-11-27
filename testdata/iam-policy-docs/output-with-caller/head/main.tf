data "aws_iam_policy_document" "root" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
output "json" {
  value = data.aws_iam_policy_document.root.json
}
