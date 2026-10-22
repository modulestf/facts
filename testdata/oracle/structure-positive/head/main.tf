provider "aws" {
  alias  = "replica"
  region = "eu-west-1"
}

locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  name = var.name

  count = local.create ? 1 : 0

  tags = var.tags

  retention_days = var.retention_days
}

resource "aws_iam_role" "extra" {
  count = local.create ? 1 : 0

  name                 = "${var.name}-extra"
  assume_role_policy   = var.assume_role_policy
  permissions_boundary = "arn:aws:iam::123456789012:policy/boundary"
}
