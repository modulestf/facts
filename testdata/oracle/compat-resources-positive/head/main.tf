locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  for_each = local.create ? toset([var.name]) : toset([])

  name = each.key

  tags = var.tags
}

resource "aws_demo_gadget" "main" {
  count = local.create ? 1 : 0

  name = var.name
}

data "aws_iam_policy_document" "this" {
  statement {
    actions   = ["demo:GetWidget"]
    resources = ["*"]
  }
}

resource "aws_demo_widget_policy" "this" {
  count = local.create ? 1 : 0

  widget_id = aws_demo_widget.this[var.name].id
  policy    = data.aws_iam_policy_document.this.json
}
