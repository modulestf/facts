locals {
  region = "eu-west-1"
}
data "aws_availability_zones" "z" {
  region = local.region
}
