provider "aws" {
  region = "eu-west-1"
}

module "this" {
  source = "../../"

  name = "basic"
}
