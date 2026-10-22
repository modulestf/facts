locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_gadget" "this" {
  count = local.create ? 1 : 0

  name = var.name
}

data "aws_iam_policy_document" "this" {
  statement {
    actions   = ["demo:GetWidget", "demo:PutWidget"]
    resources = ["*"]
  }
}

resource "aws_demo_widget_policy" "this" {
  count = local.create ? 1 : 0

  widget_id = aws_demo_widget.this[0].id
  policy    = data.aws_iam_policy_document.this.json
}
