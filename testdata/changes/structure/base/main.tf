resource "aws_lb" "nested" {
  name = var.name
  access_logs {
    enabled = true
  }
}

resource "aws_lb" "dynamic" {
  name = var.name
}

resource "aws_lb" "added_attr" {
  name = var.name
}

resource "aws_lb" "comment" {
  name    = var.name
  subnets = var.subnets
}

resource "aws_lb" "block_comment" {
  name = var.name
}

resource "aws_lb" "lifecycle" {
  name = var.name
  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_lb" "meta" {
  count = var.create ? 1 : 0
  name  = var.name
}

resource "aws_lb" "two" {
  name            = var.name
  security_groups = var.sg
}

resource "aws_lb" "trailing" {
  name = var.name
}
