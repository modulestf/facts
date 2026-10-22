resource "aws_demo_widget" "this" {
  name = var.name
}

resource "aws_demo_widget" "reordered" {
  count = 1

  name = var.name

  tags = var.tags
}
