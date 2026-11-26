module "lambda" {
  source = "terraform-aws-modules/lambda/aws"
  tags   = { build_in_docker = true }
  # build_in_docker = true
  description = "build_in_docker = true"
}
