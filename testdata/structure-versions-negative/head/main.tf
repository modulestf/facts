locals {
  name     = "demo"
  has_dash = strcontains(var.name, "-")
}

module "child" {
  source = "./modules/child"

  size     = var.child_size
  settings = merge(var.child_settings, { retries = 5 })
}
