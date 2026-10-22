provider "aws" {
  region = local.region
}

locals {
  region = "eu-west-1"
}

module "this" {
  source = "../../"

  name = "example"
}
