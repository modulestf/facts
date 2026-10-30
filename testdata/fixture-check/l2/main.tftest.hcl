# The for_each key change: groups are keyed by name, and a plan shows it.
mock_provider "aws" {
  override_during = plan

  # Every computed attribute of each mocked type has a default, so the plan
  # sees the same values on every run.
  mock_data "aws_vpc" {
    defaults = {
      arn                                  = "arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0123456789abcdef0"
      cidr_block                           = "10.0.0.0/16"
      cidr_block_associations              = []
      default                              = false
      dhcp_options_id                      = "dopt-0123456789abcdef0"
      enable_dns_hostnames                 = true
      enable_dns_support                   = true
      enable_network_address_usage_metrics = false
      id                                   = "vpc-0123456789abcdef0"
      instance_tenancy                     = "default"
      ipv6_association_id                  = ""
      ipv6_cidr_block                      = ""
      ipv6_cidr_block_associations         = []
      main_route_table_id                  = "rtb-0123456789abcdef0"
      owner_id                             = "123456789012"
      region                               = "us-east-1"
      state                                = "available"
      tags                                 = {}
    }
  }

  mock_resource "aws_security_group" {
    defaults = {
      arn         = "arn:aws:ec2:us-east-1:123456789012:security-group/sg-0123456789abcdef0"
      egress      = []
      id          = "sg-0123456789abcdef0"
      ingress     = []
      name        = "fixture"
      name_prefix = ""
      owner_id    = "123456789012"
      region      = "us-east-1"
      tags_all    = {}
      vpc_id      = "vpc-0123456789abcdef0"
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
