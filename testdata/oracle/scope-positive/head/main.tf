locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "random_id" "suffix" {
  count = local.create ? 1 : 0

  byte_length = 4
}

module "network" {
  source  = "example-org/network/aws"
  version = "1.0.0"
}
