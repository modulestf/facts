resource "aws_demo_widget" "this" {
  name = var.name
}

resource "aws_kms_key" "this" {
  description = var.name
}
