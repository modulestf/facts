resource "aws_demo_widget" "this" {
  name  = var.name
  count = 1
}

provider "aws" {
  region = "eu-west-1"
}

# Example: arn:aws:iam::123456789012:policy/boundary
resource "aws_iam_role" "this" {
  count = 1

  name                 = "${var.name}-role"
  description          = "Role to learn:aws:things"
  permissions_boundary = "arn:${local.partition}:iam::${local.account_id}:policy/boundary"

  tags = var.tags
}

locals {
  partition  = "aws"
  account_id = "123456789012"
}

resource "aws_demo_gadget" "ordered" {
  for_each = var.items

  name = each.key

  dynamic "rule" {
    for_each = var.rules
    content {}
  }

  tags = var.tags
}
