locals {
  read_actions = ["demo:GetWidget"]
  name         = "demo"
}

resource "aws_demo_widget" "this" {
  count = var.create ? 1 : 0

  name = local.name
}

resource "aws_demo_gadget" "this" {
  name = "gadget"
}

resource "aws_demo_bolt" "this" {
  for_each = var.bolts

  name = each.key
}

data "aws_demo_lookup" "old" {
  name = "lookup"
}

data "aws_iam_policy_document" "read" {
  statement {
    sid       = "Read"
    actions   = local.read_actions
    resources = ["arn:demo:one"]
  }
}

resource "aws_demo_bucket_policy" "this" {
  bucket = "demo"
  policy = data.aws_iam_policy_document.read.json
}
