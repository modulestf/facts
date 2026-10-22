provider "aws" {
  region = local.region
}

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}

locals {
  region = "eu-west-1"
}

module "this" {
  source = "../../"

  name = "example"
}

module "disabled" {
  source = "../../"

  create = false
}
