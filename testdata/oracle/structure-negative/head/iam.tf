resource "aws_iam_role" "this" {
  count = local.create ? 1 : 0

  name               = "${var.name}-role"
  assume_role_policy = var.assume_role_policy
}

# Format of the boundary ARN: arn:aws:iam::123456789012:policy/boundary
resource "aws_iam_role" "extra" {
  count = local.create ? 1 : 0

  name                 = "${var.name}-extra"
  assume_role_policy   = var.assume_role_policy
  permissions_boundary = "arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:policy/boundary"
}
