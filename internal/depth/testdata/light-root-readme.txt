kind: must-be-light
depth: light
reason: light-docs-only
-- base/README.md --
# Module
-- base/examples/complete/main.tf --
module "this" {
  source = "../.."
  bucket = "b"
  name   = "n"
}
-- base/main.tf --
resource "aws_s3_object" "this" {
  bucket  = var.bucket
  key     = "x"
  content = templatefile("${path.module}/x.tpl", { name = var.name })
}

variable "bucket" {
  type = string
}

variable "name" {
  type = string
}
-- base/x.tpl --
hello ${name}
-- head/README.md --
# Module

More text.
-- head/examples/complete/main.tf --
module "this" {
  source = "../.."
  bucket = "b"
  name   = "n"
}
-- head/main.tf --
resource "aws_s3_object" "this" {
  bucket  = var.bucket
  key     = "x"
  content = templatefile("${path.module}/x.tpl", { name = var.name })
}

variable "bucket" {
  type = string
}

variable "name" {
  type = string
}
-- head/x.tpl --
hello ${name}
