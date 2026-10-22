locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

module "child" {
  source = "./modules/child"

  create   = local.create
  size     = var.child_size
  label    = var.child_label
  settings = merge(var.child_settings, { retries = 5 })
}
