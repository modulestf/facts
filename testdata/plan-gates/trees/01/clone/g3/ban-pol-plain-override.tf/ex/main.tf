data "aws_iam_policy" "t" {
  count = var.on ? 1 : 0
  arn   = "arn:aws:iam::aws:policy/AWSXrayWriteOnlyAccess"
}
