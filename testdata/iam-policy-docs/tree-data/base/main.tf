data "local_file" "bucket" {
  filename = "bucket.txt"
}
data "external" "account" {
  program = ["sh", "account.sh"]
}
data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${data.local_file.bucket.content}/*", "arn:aws:iam::${data.external.account.result.id}:root"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child.json
}
