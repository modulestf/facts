locals {
  n = 1
}
module "mid" {
  source = "./modules/mid"
  count  = local.n
}
