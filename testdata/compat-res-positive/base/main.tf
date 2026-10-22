locals {
  read_actions = ["demo:GetWidget", "demo:ListWidgets"]
}

resource "aws_demo_widget" "this" {
  name = "widget"
}

resource "aws_demo_gadget" "this" {
  name = "gadget"
}

resource "aws_demo_thing" "this" {
  count = 2

  name = "thing-${count.index}"
}

resource "aws_demo_bolt" "this" {
  for_each = var.bolts

  name = each.key
}

data "aws_iam_policy_document" "deny" {
  statement {
    effect    = "Deny"
    actions   = ["demo:DeleteWidget"]
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "read" {
  statement {
    actions   = local.read_actions
    resources = ["*"]
  }
}

data "aws_iam_policy_document" "gone" {
  statement {
    actions   = ["demo:TagWidget"]
    resources = ["*"]
  }
}

resource "aws_demo_bucket_policy" "this" {
  bucket = "demo"
  policy = jsonencode({
    Statement = [{ Effect = "Allow", Action = ["demo:Get", "demo:Put"], Resource = "*" }]
  })
}
