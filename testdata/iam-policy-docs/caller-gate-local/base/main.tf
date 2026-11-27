locals {
  n = 1
}
module "role" {
  source = "./modules/role"
  count  = local.n
}
