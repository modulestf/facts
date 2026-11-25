variable "name" {
  type    = string
  default = "y"
}

locals {
  name = var.tags
}

module "listener" {
  source = "./modules/sub"
  port   = 81
}

resource "aws_lb" "main" {
  name = var.name
}

output "arn" {
  value = aws_lb.main.id
}
