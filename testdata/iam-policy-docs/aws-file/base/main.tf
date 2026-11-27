resource "aws_s3_object" "o" {
  bucket = "b"
  key    = "k"
  source = "obj.json"
}
data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::b/k/${aws_s3_object.o.etag}"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child.json
}
