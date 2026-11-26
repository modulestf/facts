data "aws_availability_zones" "z" {
  region = data.aws_region.current.region
}
