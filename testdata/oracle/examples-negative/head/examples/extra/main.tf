provider "aws" {
  region = "eu-west-1"
}

module "this" {
  source = "../../modules/extra"
}
