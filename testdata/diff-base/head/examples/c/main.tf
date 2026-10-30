module "log" {
  source = "../../modules/log"

  name                        = "c"
  deletion_protection_enabled = true
}