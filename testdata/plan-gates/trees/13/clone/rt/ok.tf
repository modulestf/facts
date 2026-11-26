terraform {
  required_providers {
    aws        = { source = "hashicorp/aws" }
    null       = { source = "hashicorp/null" }
    external   = { source = "hashicorp/external" }
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}
provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  default_tags {
    tags = { Name = "x" }
  }
}
provider "kubernetes" {
  host  = "https://example.invalid"
  token = "t"
}
/* a block comment with a } brace */
resource "null_resource" "x" {
  provisioner "local-exec" {
    command = "echo ${path.module}"
  }
}
data "external" "x" {
  program = ["sh", "-c", "echo {}"]
}
locals {
  f = file("${path.module}/x.txt")
  s = "${join(",", ["a"])}"
  h = <<-EOT
    café "quoted" } #
  EOT
}
module "gh" {
  source = "github.com/terraform-aws-modules/terraform-aws-s3-bucket"
}
module "reg" {
  source = "terraform-aws-modules/s3-bucket/aws"
}
module "loc" {
  source = "../"
}
check "c" {
  assert {
    condition     = true
    error_message = "x"
  }
}
