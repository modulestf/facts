locals {
  x = <<-EOT
    module "a" {
      source = "../../modules/a"
    }
    EOT
}
