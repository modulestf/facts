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
