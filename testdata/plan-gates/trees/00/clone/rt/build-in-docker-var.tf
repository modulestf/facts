module "lambda" {
  source          = "terraform-aws-modules/lambda/aws"
  build_in_docker = var.build_in_docker
}
