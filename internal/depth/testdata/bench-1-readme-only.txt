kind: bench
depth: light
-- base/README.md --
# SQS
-- base/examples/complete/main.tf --
module "this" {
  source = "../.."
  name   = "example"
}

output "arn" {
  value = module.this.arn
}
-- base/main.tf --
resource "aws_sqs_queue" "this" {
  name                      = var.name
  message_retention_seconds = 60
}
-- base/outputs.tf --
output "arn" {
  value = aws_sqs_queue.this.arn
}
-- base/variables.tf --
variable "name" {
  type = string
}
-- base/versions.tf --
terraform {
  required_version = ">= 1.5.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}
-- head/README.md --
# SQS

Usage notes.
-- head/examples/complete/main.tf --
module "this" {
  source = "../.."
  name   = "example"
}

output "arn" {
  value = module.this.arn
}
-- head/main.tf --
resource "aws_sqs_queue" "this" {
  name                      = var.name
  message_retention_seconds = 60
}
-- head/outputs.tf --
output "arn" {
  value = aws_sqs_queue.this.arn
}
-- head/variables.tf --
variable "name" {
  type = string
}
-- head/versions.tf --
terraform {
  required_version = ">= 1.5.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}
-- want.json --
{
  "format": 1,
  "depth": "light",
  "reasons": [
    "light-docs-only"
  ],
  "checks_out_of_scope": [
    "A",
    "B",
    "D",
    "G-ladder"
  ],
  "examples_selected": [
    "examples/complete"
  ],
  "examples_out_of_scope": [],
  "touched_types": [],
  "walk": {
    "complete": true,
    "unwalked": 0
  },
  "limits": {
    "tf_files": 0,
    "changed_lines": 2,
    "changed_blocks": 0,
    "selected_examples": 1
  },
  "schema_providers": [
    "base:.:aws=hashicorp/aws:\u003e=5.0",
    "head:.:aws=hashicorp/aws:\u003e=5.0"
  ],
  "schema_types": [
    ".:resource:aws_sqs_queue:hashicorp/aws"
  ],
  "schema_locks": []
}
