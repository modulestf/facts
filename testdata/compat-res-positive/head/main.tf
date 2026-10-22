locals {
  read_actions = ["demo:GetWidget"]
}

moved {
  from = aws_demo_widget.this
  to   = module.child.aws_demo_widget.this
}

module "child" {
  source = "./modules/child"
}

removed {
  from = aws_demo_gadget.this

  lifecycle {
    destroy = false
  }
}

resource "aws_demo_thing" "this" {
  count = 1

  name = "thing-${count.index}"
}

resource "aws_demo_bolt" "this" {
  for_each = var.bolt_map

  name = each.key
}

data "aws_iam_policy_document" "deny" {
  statement {
    effect    = "Deny"
    actions   = ["demo:DeleteWidget", "demo:PutWidget"]
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "read" {
  statement {
    actions   = local.read_actions
    resources = ["*"]
  }
}

resource "aws_demo_bucket_policy" "this" {
  bucket = "demo"
  policy = jsonencode({
    Statement = [{ Effect = "Allow", Action = ["demo:Get"], Resource = "*" }]
  })
}
