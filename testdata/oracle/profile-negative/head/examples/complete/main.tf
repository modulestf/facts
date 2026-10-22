provider "aws" {
  region = local.region
}

locals {
  region = "eu-west-1"

  zone_names = {
    "us-west-2" = "oregon"
  }

  log_bucket_arn = "arn:aws:s3:::logs-us-west-2"
}

module "this" {
  source = "../../"

  name = "example"
}

module "disabled" {
  source = "../../"

  create = false
}
