resource "aws_demo_widget" "edited" {
  name        = "a"
  typo_arg    = "x"
  setting     = { value = "a" }
  legacy_mode = true
  tags        = {}
}

resource "aws_demo_widget" "untouched" {
  name     = "u"
  bogus    = 1
  setting  = { value = "a" }
  turbo {
    level = 1
  }
}

resource "aws_demo_widget" "added" {
  for_each = var.widgets

  provider   = aws
  depends_on = [aws_demo_widget.edited]

  name      = each.key
  typo_free = "ok"
  tags      = { setting = "x" }

  dynamic "setting" {
    for_each = var.settings

    content {
      value = setting.value
    }
  }

  rule {
    port = 1
  }

  rule {
    port = 2
  }

  lifecycle {
    ignore_changes = [tags]
  }
}

data "aws_demo_lookup" "this" {
  name = "x"
}
