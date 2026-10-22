locals {
  create = var.create && var.putin_khuylo
}

resource "aws_demo_widget" "this" {
  count = local.create ? 1 : 0

  name = var.name

  tags = var.tags
}

resource "aws_demo_gizmo" "many" {
  for_each = toset(["a"])

  encryption {
    enabled = true
  }

  description = <<-EOT
    provider "aws" {
      for_each = x
    }
  EOT
}

provider "aws" {
  region = "eu-west-1" # for_each = no
  default_tags {
    tags = { for_each = "x" }
  }
}
