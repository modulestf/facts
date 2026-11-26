variable "ladder_esample_0" {
  type = string
}

variable "ladder_mode_0" {
  type = string
}

variable "ladder_sample_0" {
  type = list(string)
}

variable "ladder_unk_0" {
  type = list(string)
}

variable "ladder_val_0" {
  type = list(string)
}

resource "terraform_data" "ladder_u_0" {
  input = var.ladder_sample_0
}

resource "terraform_data" "ladder_ue_0" {
  input = var.ladder_esample_0
}

locals {
  ladder_in_0 = var.ladder_mode_0 == "unknown" ? terraform_data.ladder_u_0.output : var.ladder_mode_0 == "known" ? tolist([for k, v in var.ladder_val_0 : contains(var.ladder_unk_0, tostring(k)) ? terraform_data.ladder_ue_0.output : v]) : null
}

output "canary_ladder_u_0" {
  value = terraform_data.ladder_u_0.output
}

output "canary_ladder_ue_0" {
  value = terraform_data.ladder_ue_0.output
}

output "canary_known" {
  value = "known"
}

