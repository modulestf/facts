provider "aws" {
  region = local.region
}

locals {
  region = "ap-southeast-2"
}

module "this" {
  source = "../../"

  name = "example"
}

module "disabled" {
  source = "../../"

  create = false
}
