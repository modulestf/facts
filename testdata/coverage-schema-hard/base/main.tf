resource "aws_demo_widget" "edited" {
  provider = demo

  name  = "e"
  bogus = 1

  listener {
    port  = 1
    weird = 2
  }
}
