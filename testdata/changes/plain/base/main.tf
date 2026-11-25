resource "aws_lb" "this" {
  security_groups = concat(aws_security_group.this[*].id, var.security_groups)
}

resource "aws_demo" "lookup" {
  value = var.v
}

resource "aws_demo" "try" {
  value = var.v
}

resource "aws_demo" "merge" {
  value = var.v
}

resource "aws_demo" "conditional" {
  value = var.v
}

resource "aws_demo" "for" {
  value = var.v
}

resource "aws_demo" "file" {
  value = var.v
}

resource "aws_demo" "templatefile" {
  value = var.v
}

resource "aws_demo" "jsondecode" {
  value = var.v
}

resource "aws_demo" "count_ref" {
  value = var.v
}

resource "aws_demo" "each_ref" {
  value = var.v
}

resource "aws_demo" "self_ref" {
  value = var.v
}

resource "aws_demo" "operator" {
  value = var.v
}

resource "aws_demo" "parens" {
  value = var.v
}

resource "aws_demo" "directive" {
  value = var.v
}

resource "aws_demo" "expand" {
  value = var.v
}

resource "aws_demo" "heredoc" {
  value = var.v
}

resource "aws_demo" "provider_func" {
  value = var.v
}

resource "aws_demo" "negative" {
  value = var.v
}

resource "aws_demo" "literals" {
  value = var.v
}

resource "aws_demo" "object" {
  value = var.v
}

resource "aws_demo" "object_key_expr" {
  value = var.v
}

resource "aws_demo" "splat" {
  value = var.v
}

resource "aws_demo" "index" {
  value = var.v
}

resource "aws_demo" "interp" {
  value = var.v
}

resource "aws_demo" "funcs" {
  value = var.v
}
