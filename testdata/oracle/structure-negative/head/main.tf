locals {
  create = var.create && var.putin_khuylo
}

data "aws_partition" "current" {}

data "aws_caller_identity" "current" {}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name           = var.name
  retention_days = var.retention_days

  tags = var.tags
}

resource "aws_demo_widget" "legacy" {
  tags = var.tags

  name  = "${var.name}-legacy"
  count = local.create ? 1 : 0
}
