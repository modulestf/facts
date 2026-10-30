# The for_each key change: groups are keyed by name, and a plan shows it.
mock_provider "aws" {
  override_during = plan
  mock_data "aws_vpc" {
    defaults = {
      cidr_block = "10.0.0.0/16"
    }
  }

  mock_resource "aws_security_group" {
    defaults = {
      id  = "sg-0123456789abcdef0"
      arn = "arn:aws:ec2:us-east-1:123456789012:security-group/sg-0123456789abcdef0"
    }
  }
}

variables {
  groups = [
    { name = "web", ports = [443] },
    { name = "db", ports = [5432, 5433] },
  ]
}

run "keys_by_name" {
  command = plan

  plan_options {
    mode = normal
  }

  override_data {
    target = data.aws_vpc.this
    values = {
      cidr_block = "10.1.0.0/16"
    }
  }

  assert {
    condition     = toset(keys(aws_security_group.this)) == toset(["db", "web"])
    error_message = "groups are keyed by name"
  }

  assert {
    condition     = length(aws_security_group.this["db"].ingress) == 2
    error_message = "the db group has one rule a port"
  }

  assert {
    condition     = output.group_names == tolist(["db", "web"])
    error_message = "group names"
  }
  assert {
    condition     = aws_security_group.this["web"].id == "sg-0123456789abcdef0"
    error_message = "mock id at plan"
  }
}
