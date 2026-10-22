locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  for_each = local.create ? toset(["default"]) : toset([])

  name = var.name

  tags = var.tags
}

moved {
  from = aws_demo_widget.this[0]
  to   = aws_demo_widget.this["default"]
}

resource "aws_demo_gadget" "main" {
  count = local.create ? 1 : 0

  name = var.name
}

moved {
  from = aws_demo_gadget.this
  to   = aws_demo_gadget.main
}

data "aws_iam_policy_document" "this" {
  statement {
    actions   = ["demo:GetWidget", "demo:PutWidget", "demo:ListWidgets"]
    resources = ["*"]
  }
}

resource "aws_demo_widget_policy" "this" {
  count = local.create ? 1 : 0

  widget_id = aws_demo_widget.this["default"].id
  policy    = data.aws_iam_policy_document.this.json
}
