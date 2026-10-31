resource "aws_instance" "x" {
  vpc_security_group_ids = var.ids
  ami                    = local.ami
}

variable "ids" {
  type = list(string)
}

variable "ami" {
  type = string
}
