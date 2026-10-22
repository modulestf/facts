resource "aws_demo_extra" "this" {
  name = "extra"
}

resource "aws_cloudwatch_log_group" "this" {
  name = "extra"
}

data "aws_demo_lookup" "this" {}
