terraform {
  required_providers {
    example = {
      source = "Acme/Example"
    }
    awsalt = {
      source = "acme/aws-fork"
    }
  }
}

resource "example_widget" "this" {}

resource "aws_fork_thing" "this" {
  provider = awsalt.east
}
