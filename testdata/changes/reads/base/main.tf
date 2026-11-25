resource "aws_lb" "this" {
  count   = 1
  name    = var.name
  subnets = var.subnets
  tags    = var.tags
  idle    = var.idle
  timeout = var.timeout
  arn_x   = var.arn_x
}

resource "aws_lb" "whole" {
  name = var.name
}

resource "aws_lb" "single" {
  key      = var.key
  other    = var.other
  selfread = var.selfread
  provisioner "local-exec" {
    command = "echo ${self.selfread}"
  }
}

resource "aws_lb" "dyn" {
  name = var.name
}

resource "aws_lb" "numeric" {
  name = var.name
}

data "aws_ami" "this" {
  owners = var.owners
}
