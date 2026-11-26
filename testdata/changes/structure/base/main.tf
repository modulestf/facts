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

resource "aws_lb" "meta_count" {
  count = var.n_a
  name  = var.name
}

resource "aws_lb" "meta_for_each" {
  for_each = var.set_a
  name     = var.name
}

resource "aws_lb" "meta_provider" {
  provider = aws.a
  name     = var.name
}

data "aws_ami" "meta_depends_on" {
  depends_on = [aws_lb.meta_count_a]
  owners     = var.owners
}

module "meta_module" {
  source = "./m"
  count  = var.n_a
}
