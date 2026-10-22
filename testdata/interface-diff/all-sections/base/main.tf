resource "aws_demo_thing" "this" {
  count = 1
  name  = var.name
}

data "aws_region" "current" {}

moved {
  from = aws_demo_thing.old
  to   = aws_demo_thing.this
}

module "child" {
  source  = "terraform-aws-modules/demo/aws"
  version = "~> 1.0"
}

module "gone" {
  source = "./modules/old"
}

output "id" {
  value = aws_demo_thing.this[0].id
}
