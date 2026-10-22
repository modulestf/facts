resource "aws_s3_bucket" "this" {
  bucket = "example"
}

resource "aws_s3_bucket" "east" {
  provider = aws.east
  bucket   = "example-east"
}

resource "aws_gone_thing" "this" {
  name = "example"
}

resource "aws_new_thing" "this" {}

resource "aws_nowhere" "this" {}

data "aws_iam_policy_document" "this" {}
