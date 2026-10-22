resource "aws_demo_widget" "new" {
  name        = var.name
  description = var.description

  config = var.config

  dynamic "listener" {
    for_each = var.listeners

    content {
      port  = listener.value.port
      proto = listener.value.proto
    }
  }

  dynamic "extra" {
    for_each = var.extra == null ? [] : [var.extra]

    content {
      value = extra.value
    }
  }
}

resource "aws_demo_widget" "counted" {
  count = var.create ? 1 : 0

  name        = var.name
  description = var.description

  config = {
    mode  = var.mode
    level = var.level
  }

  listener {
    port  = 443
    proto = var.proto
  }

  extra {
    value = var.extra_value
  }
}

resource "aws_demo_widget" "edited" {
  name = "${var.name}-edited"

  config = var.config
}

data "aws_demo_lookup" "this" {
  name = var.lookup
}
