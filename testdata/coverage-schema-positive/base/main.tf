resource "aws_demo_widget" "this" {
  count = 1

  name     = "a"
  typo_arg = "x"
  setting  = { value = "a" }
}
