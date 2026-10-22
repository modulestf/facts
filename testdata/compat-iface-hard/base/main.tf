locals {
  max_window = 24
}

resource "aws_demo_widget" "this" {
  name = var.name
}
