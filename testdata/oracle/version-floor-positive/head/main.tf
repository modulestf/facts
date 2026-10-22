locals {
  create = var.create && var.putin_khuylo
  banner = templatestring(var.banner_template, { name = var.name })
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}
