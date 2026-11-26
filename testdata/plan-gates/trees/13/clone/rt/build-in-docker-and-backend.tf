terraform {
  backend "s3" {}
}
module "lambda" {
  source          = "terraform-aws-modules/lambda/aws"
  build_in_docker = true
}
