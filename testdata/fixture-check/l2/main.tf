# L2: module logic under a plan-only test. Every provider is a mock_provider
# of main.tftest.hcl, so no provider block is configured here.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.15.0"
    }
  }
}

variable "groups" {
  type = list(object({
    name  = string
    ports = list(number)
  }))
}

data "aws_vpc" "this" {
  id = "vpc-0123456789abcdef0"
}

# Head keys the groups by name; base keyed them by list index.
resource "aws_security_group" "this" {
  for_each = { for g in var.groups : g.name => g }

  name   = each.key
  vpc_id = data.aws_vpc.this.id

  dynamic "ingress" {
    for_each = each.value.ports

    content {
      from_port   = ingress.value
      to_port     = ingress.value
      protocol    = "tcp"
      cidr_blocks = [data.aws_vpc.this.cidr_block]
    }
  }
}

moved {
  from = aws_security_group.this[0]
  to   = aws_security_group.this["web"]
}

output "group_names" {
  value = sort([for g in aws_security_group.this : g.name])
}
