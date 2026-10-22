locals {
  new_arn = aws_demo_widget.new.arn
}

resource "aws_demo_widget" "new" {
  description = var.description

  config = {
    level = 1
  }

  dynamic "listener" {
    for_each = []

    content {
      port = 1
    }
  }

  extra {}
}

resource "aws_demo_widget" "edited" {
  name = null

  config = var.config
}

data "aws_demo_lookup" "this" {
  filter = "x"
}
