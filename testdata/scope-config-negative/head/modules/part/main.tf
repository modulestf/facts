resource "aws_demo_part" "this" {
  name = "part"
}

module "sibling" {
  source = "../extra"
}
