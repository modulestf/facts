data "aws_iam_policy_document" "d" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "merged" {
  source_json = data.aws_iam_policy_document.d.json
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.merged.json
}
