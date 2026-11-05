module "net" {
  source = "../../modules/network"
  cidr   = "10.0.0.0/16"
  region = "eu-west-1"
}
