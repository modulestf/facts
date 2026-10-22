resource "aws_demo_widget" "child" {
  count = var.create ? 1 : 0

  name    = var.label
  size    = var.size
  mode    = var.settings.mode
  retries = var.settings.retries
}
