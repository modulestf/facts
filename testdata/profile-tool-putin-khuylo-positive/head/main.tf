locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

locals {
  create_gadget = var.create && var.name != ""
}

resource "aws_demo_gadget" "a" {
  count = local.create_gadget ? 1 : 0
}

resource "aws_demo_gadget" "b" {
  for_each = local.create_gadget ? toset(["x"]) : toset([])
}

resource "aws_demo_gizmo" "direct" {
  count = var.create ? 1 : 0
}
