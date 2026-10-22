locals {
  create = var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_gadget" "extra" {
  name        = var.extra_name
  description = var.extra_description

  dynamic "rule" {
    for_each = var.extra_rules

    content {
      port = rule.value.port
    }
  }

  tags = var.tags
}
