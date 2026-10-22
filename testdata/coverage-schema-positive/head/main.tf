resource "aws_demo_widget" "this" {
  count = 1

  name      = "a"
  typo_arg  = "x"
  setting   = { value = "a" }
  new_typo  = "y"
  fast_path = true
  rule      = { port = 80 }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_demo_widget" "nested" {
  provider = aws.west

  name = "n"

  setting {
    value = "a"
    mode  = "fast"
    bogus = 1
  }

  setting {
    value = "b"
  }

  dynamic "rule" {
    for_each = var.ports

    content {
      port  = rule.value
      color = "red"
    }
  }

  old_rule {
    port = 1
  }

  turbo {
    level = 3
  }
}

data "aws_demo_lookup" "this" {
  name   = "x"
  filter = "y"
}
