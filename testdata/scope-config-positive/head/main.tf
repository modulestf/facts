resource "aws_demo_widget" "this" {
  name = var.name
}

module "label" {
  source  = "git::https://example.com/label.git"
  version = "1.0.0"
}

resource "random_id" "suffix" {
  byte_length = 4
}

check "endpoint" {
  data "http" "health" {
    url = "https://example.com/health"
  }
}

module "escape" {
  source = "../outside"
}

module "double" {
  source = "./modules/part//sub"
}
