module "lambda" {
  source          = "terraform-aws-modules/lambda/aws"
  runtime         = "python3.12"
  build_in_docker = true # the packaging calls Docker
}
