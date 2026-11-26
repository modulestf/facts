locals {
  n = 2
}
module "mid" {
  source = "./modules/mid"
  count  = local.n
}
