locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_widget" "each" {
  for_each = local.create ? var.widgets : {}

  name = each.value

  tags = var.tags
}
