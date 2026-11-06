module "network" {
  source = "../../infra/modules/network"

  cidr = "10.0.0.0/16"
  name = "stage"
}
