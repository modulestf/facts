resource "aws_demo_widget" "this" {
  name      = "a"
  bogus     = 1
  fast_path = true
}

resource "aws_demo_widget" "added" {
  name      = "b"
  fast_path = true
}
