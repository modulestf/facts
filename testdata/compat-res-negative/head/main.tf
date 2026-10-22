locals {
  read_actions = ["demo:GetWidget"]
  name         = "demo-renamed"
}

resource "aws_demo_widget" "this" {
  name = local.name
}

moved {
  from = aws_demo_gadget.this
  to   = module.child.aws_demo_gadget.this
}

module "child" {
  source = "./modules/child"
}

resource "aws_demo_bolt" "this" {
  for_each = var.bolts

  name = "${each.key}-bolt"
}

data "aws_iam_policy_document" "read" {
  statement {
    sid       = "ReadWidgets"
    actions   = local.read_actions
    resources = ["arn:demo:one", "arn:demo:two"]
  }

  statement {
    actions   = ["demo:ListWidgets"]
    resources = ["*"]
  }
}

resource "aws_demo_bucket_policy" "this" {
  bucket = "demo-bucket"
  policy = data.aws_iam_policy_document.read.json
}
