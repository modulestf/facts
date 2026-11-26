data "aws_iam_policy_document" "widget" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "widget" {
  count  = 0
  policy = data.aws_iam_policy_document.widget.json
}
resource "aws_iam_policy" "admin" {
  policy = <<-EOT
    {
      "Version": "2012-10-17",
      "Statement": [{"Effect": "Allow", "Action": "*", "Resource": "*"}]
    }
  EOT
}
