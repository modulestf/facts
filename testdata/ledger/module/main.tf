data "aws_example_region" "current" {}

resource "aws_example_bucket" "this" {
  count = var.create ? 1 : 0

  name = var.name
  tags = var.tags

  dynamic "logging" {
    for_each = var.logging != null ? [var.logging] : []

    content {
      target = logging.value.target
    }
  }
}

resource "aws_example_bucket" "replica" {
  count = var.create && var.replica ? 1 : 0

  name = "${var.name}-replica"
}
