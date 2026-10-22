resource "aws_demo_widget" "edited" {
  provider = demo

  name  = "e"
  bogus = 2

  listener {
    port = 0
  }

  listener {
    port  = 1
    weird = 2
  }
}

resource "aws_demo_widget" "values" {
  provider = demo

  name = "v"

  config = {
    mode   = "a"
    level  = 2
    legacy = true
    bogus  = 1
  }

  rules = [
    { port = 1, proto = "tcp" },
    { port = 2, colour = "x" },
  ]

  peers = {
    a = { addr = "x" }
    b = { nope = "y" }
  }

  tags {
    a = 1
  }
}

resource "aws_demo_widget" "computed" {
  provider = demo.west

  name   = "c"
  config = { (var.key) = 1 }
  rules  = var.rules
}

resource "aws_demo_widget" "blocks" {
  provider = demo

  name = "b"

  listener {
    port  = 1
    weird = 2
  }

  rules {
    port  = 1
    weird = 2
  }

  setting {
    value = "a"
  }

  dynamic "setting" {
    for_each = var.s == null ? [] : [var.s]

    content {
      value = setting.value
    }
  }
}

resource "aws_demo_widget" "dynamic_one" {
  provider = demo

  name = "d"

  dynamic "setting" {
    for_each = var.s == null ? [] : [var.s]

    content {
      value = setting.value
    }
  }
}

resource "aws_demo_widget" "dynamic_unknown" {
  provider = demo

  name = "u"

  dynamic "setting" {
    for_each = var.settings

    content {
      value = setting.value
    }
  }
}

resource "aws_demo_turbo" "this" {
  provider = demo

  name = "t"
  size = 1
}
