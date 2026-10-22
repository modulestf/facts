module "this" {
  source = "../../"
}

module "disabled" {
  source = "../../"

  create = false
}
