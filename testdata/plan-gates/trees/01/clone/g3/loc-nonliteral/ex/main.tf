locals {
  region = var.region
}
data "aws_availability_zones" "z" {
  region = local.region
}
