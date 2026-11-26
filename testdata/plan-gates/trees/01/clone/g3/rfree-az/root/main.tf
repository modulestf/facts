module "fs" {
  source = "../mod"
  region = var.region
}
variable "region" {
  default = null
}
