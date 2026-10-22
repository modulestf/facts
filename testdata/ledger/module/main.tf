data "aws_example_region" "current" {}

resource "aws_example_bucket" "this" {
  count = var.create ? 1 : 0

  name = var.name
  tags = var.tags

  encryption {
    algorithm = var.encryption_algorithm
  }

  dynamic "logging" {
    for_each = var.logging != null ? [var.logging] : []

    content {
      target = logging.value.target
      prefix = logging.value.prefix

      dynamic "filter" {
        for_each = logging.value.filter_suffixes

        content {
          suffix = filter.value
        }
      }
    }
  }
}

resource "aws_example_bucket" "replica" {
  count = var.create && var.replica ? 1 : 0

  name = "${var.name}-replica"

  encryption {
    algorithm = var.encryption_algorithm
  }
}
