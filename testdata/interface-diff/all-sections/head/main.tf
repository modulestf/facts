resource "aws_demo_thing" "this" {
  count    = 1
  provider = aws.replica
  name     = "${var.name}-x"
}

data "aws_region" "current" {}

data "aws_caller_identity" "current" {}

moved {
  from = aws_demo_thing.old
  to   = aws_demo_thing.this[0]
}

module "child" {
  source  = "terraform-aws-modules/demo/aws"
  version = "~> 2.0"
}

output "id" {
  description = "ID of the thing"
  value       = aws_demo_thing.this[0].id
  sensitive   = true
}
