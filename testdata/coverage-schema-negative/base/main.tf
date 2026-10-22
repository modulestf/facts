resource "aws_demo_widget" "edited" {
  name        = "a"
  typo_arg    = "x"
  setting     = { value = "a" }
  legacy_mode = true
}

resource "aws_demo_widget" "untouched" {
  name     = "u"
  bogus    = 1
  setting  = { value = "a" }
  turbo {
    level = 1
  }
}
