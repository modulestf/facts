locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_widget" "extra" {
  count = local.create ? 1 : 0

  bogus_flag  = true
  legacy_mode = var.extra_legacy_mode
  fast_path   = var.extra_fast_path

  setting = {
    value = "a"
  }

  tags = var.tags
}
