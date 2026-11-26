variable "x" { default = "" }
data "aws_iam_policy_document" "transformed" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "conditional" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
data "aws_iam_policy_document" "dynamic" {
  dynamic "statement" {
    for_each = var.x == "" ? [] : [1]
    content {
      actions   = ["s3:GetObject"]
      resources = ["*"]
    }
  }
}
data "aws_iam_policy_document" "list_from_local" {
  statement {
    actions   = local.actions
    resources = ["*"]
  }
}
locals {
  actions = ["s3:GetObject"]
}
resource "aws_iam_policy" "a" {
  policy = replace(data.aws_iam_policy_document.transformed.json, "s3", "ec2")
}
resource "aws_iam_policy" "b" {
  policy = var.x != "" ? var.x : data.aws_iam_policy_document.conditional.json
}
resource "aws_iam_policy" "c" {
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = ["s3:GetObject"]
      Resource  = "arn:aws:s3:::${var.x}/*"
      Condition = { Bool = { "aws:SecureTransport" = true } }
    }]
  })
}
resource "aws_iam_policy" "d" {
  policy = <<-EOT
    {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "${var.x}", "Resource": "*"}]}
  EOT
}
resource "aws_iam_policy" "e" {
  policy = file("${path.module}/policy.json")
}
