locals {
  n = 2
}
module "role" {
  source = "./modules/role"
  count  = local.n
}
