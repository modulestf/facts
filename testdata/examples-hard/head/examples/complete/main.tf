module "this" {
  source = "../../"
}

module "disabled" {
  source = "../../"

  create = var.create_disabled
}

variable "create_disabled" {
  type    = bool
  default = false
}
