module "role" {
  source = "./modules/role"
  create = file("flag") == "yes"
}
