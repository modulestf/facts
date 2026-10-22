resource "aws_iam_role" "this" {
  count = var.create ? 1 : 0

  name               = var.name
  assume_role_policy = var.assume_role_policy
}
