resource "aws_instance" "x" {
  vpc_security_group_ids = compact(var.ids)
  ami                    = var.ami
}

variable "ids" {
  type = list(string)
}

variable "ami" {
  type = string
}
