resource "aws_s3_bucket" "example" {
  bucket = "example"
}

data "aws_region" "current" {}
