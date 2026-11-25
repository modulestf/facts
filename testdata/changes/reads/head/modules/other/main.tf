resource "aws_lb" "this" {
  name = "other"
}

output "idle" {
  value = aws_lb.this.idle
}
