data "aws_iam_policy" "t" {
  for_each = toset(["a"])
  arn      = "arn:aws:iam::aws:policy/X"
}
