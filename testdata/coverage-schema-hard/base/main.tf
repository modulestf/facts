resource "aws_demo_widget" "edited" {
  provider = demo

  name = "e"

  rules {
    port  = 1
    weird = 2
  }
}
