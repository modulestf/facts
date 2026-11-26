data "aws_iam_policy" "t" {
  provider = aws.other
  arn      = "arn:aws:iam::aws:policy/X"
}
