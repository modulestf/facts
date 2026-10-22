resource "aws_iam_role" "this" {
  count = local.create ? 1 : 0

  name               = "${var.name}-role"
  assume_role_policy = var.assume_role_policy
}
