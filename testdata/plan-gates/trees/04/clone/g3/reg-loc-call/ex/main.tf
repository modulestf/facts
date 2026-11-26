module "m" {
  source = "../mod"
  region = local.region
}
locals {
  region = "eu-west-1"
}
