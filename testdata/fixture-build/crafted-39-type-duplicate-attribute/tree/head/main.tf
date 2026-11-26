variable "ladder_mode_0" {
  type = string
}

variable "ladder_sample_0" {
  type = object({a = string, a = number})
}

variable "ladder_val_0" {
  type = object({a = string, a = number})
}

resource "terraform_data" "ladder_u_0" {
  input = var.ladder_sample_0
}

locals {
  ladder_in_0 = var.ladder_mode_0 == "unknown" ? terraform_data.ladder_u_0.output : var.ladder_mode_0 == "known" ? var.ladder_val_0 : null
}

output "canary_ladder_u_0" {
  value = terraform_data.ladder_u_0.output
}

output "canary_known" {
  value = "known"
}

