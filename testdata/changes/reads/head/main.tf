resource "aws_lb" "this" {
  count   = 1
  name    = var.name2
  subnets = var.subnet_ids
  tags    = var.all_tags
  idle    = var.idle2
  timeout = var.timeout2
  arn_x   = var.arn_y
}

resource "aws_lb" "whole" {
  name = var.name2
}

resource "aws_lb" "single" {
  key      = var.key2
  other    = var.other2
  selfread = var.selfread2
  provisioner "local-exec" {
    command = "echo ${self.selfread}"
  }
}

resource "aws_lb" "dyn" {
  name = var.name2
}

resource "aws_lb" "numeric" {
  name = var.name2
}

data "aws_ami" "this" {
  owners = var.owners2
}

resource "aws_lb" "alias_name" {
  name  = var.name_b
  other = var.other_b
}

resource "aws_lb" "alias_whole" {
  name = var.name_b
}

resource "aws_lb" "keyed" {
  count = 1
  name  = var.name_b
}

resource "aws_lb" "in_each" {
  name = var.name_b
}

resource "aws_lb" "e" {
  count = 1
  foo   = var.foo_b
  bar   = var.bar_b
}

resource "aws_lb" "fe" {
  for_each = var.set
  name     = var.name_b
  other    = var.other_b
}

resource "aws_lb" "for_expr" {
  for_each = var.set
  id       = var.id_b
  name     = var.name_b
}

resource "aws_s3_bucket" {
  bucket = var.bucket_b
}
