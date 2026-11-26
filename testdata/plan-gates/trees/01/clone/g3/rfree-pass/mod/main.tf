locals {
  region = try(data.aws_region.current[0].region, "")
}
data "aws_region" "current" {
  count  = var.create ? 1 : 0
  region = var.region
}
data "aws_service_principal" "efs" {
  service_name = "elasticfilesystem"
  region       = local.region
}
