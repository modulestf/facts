resource "aws_demo_widget" "this" {
  name = var.name
}

module "label" {
  source  = "terraform-aws-modules/label/aws"
  version = "1.0.0"
}
