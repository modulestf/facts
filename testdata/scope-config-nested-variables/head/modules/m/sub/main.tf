variable "x" {
  type = string
}

output "y" {
  value = local.z
}

locals {
  z = var.x
}
