resource "aws_demo_widget" "this" {
  name = var.name
}

module "label" {
  source  = "terraform-aws-modules/label/aws"
  version = "1.1.0"
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "terraform_data" "marker" {
  input = var.name
}

resource "aws_demo_widget" "west" {
  provider = aws.west
  name     = var.name
}

module "extra" {
  source = "./modules/extra"
}

module "part" {
  source = "./modules/part/"
}
