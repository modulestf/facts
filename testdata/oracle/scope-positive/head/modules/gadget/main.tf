resource "aws_demo_gadget" "this" {
  count = var.create ? 1 : 0

  name = var.name
}
