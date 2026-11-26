data "aws_iam_policy" "t" {
  count = (
    1
  )
  arn = "arn:aws:iam::aws:policy/X"
}
