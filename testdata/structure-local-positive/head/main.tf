resource "aws_demo_widget" "this" {
  name = var.name
}

provider "aws" {
  alias  = "replica"
  region = "eu-west-1"
}

locals {
  bucket_arn = "arn:${local.partition}:s3:::${var.name}"
}

data "aws_iam_policy_document" "extra" {
  statement {
    resources = ["arn:aws:s3:::${var.name}/*"]
  }
}

resource "aws_demo_gadget" "this" {
  provider = aws.replica

  name       = var.name
  target_arn = "arn:${local.partition}:sns:eu-west-1:${local.account_id}:topic"
  policy     = <<-EOT
    {"Resource": "arn:${local.partition}:sqs:${local.region}:123456789012:queue"}
  EOT

  tags = var.tags
}

module "child" {
  source = "./modules/child"

  providers = {
    aws = aws.replica
  }
}
