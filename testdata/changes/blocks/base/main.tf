variable "name" {
  type    = string
  default = "x"
}

locals {
  name = var.name
}

module "listener" {
  source = "./modules/sub"
  port   = 80
}

resource "aws_lb" "this" {
  name = var.name
}

data "aws_iam_policy_document" "this" {
  statement {
    actions = ["s3:GetObject"]
  }
}

output "arn" {
  value = aws_lb.this.arn
}
