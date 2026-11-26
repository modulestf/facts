locals {
  create = var.create && var.putin_khuylo

  extra_names = local.create ? ["a", "b"] : []
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_gadget" "extra" {
  count = local.create ? 1 : 0

  name = "${var.name}-extra"
}

resource "aws_demo_gadget" "named" {
  for_each = toset([for n in local.extra_names : "${var.name}-${n}"])

  name = each.value
}
