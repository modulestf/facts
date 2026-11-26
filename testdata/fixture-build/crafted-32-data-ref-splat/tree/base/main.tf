variable "ladder_esample_0" {
  type = string
}

variable "ladder_esample_1" {
  type = string
}

variable "ladder_mode_0" {
  type = string
}

variable "ladder_mode_1" {
  type = string
}

variable "ladder_mode_2" {
  type = string
}

variable "ladder_sample_0" {
  type = list(string)
}

variable "ladder_sample_1" {
  type = list(string)
}

variable "ladder_sample_2" {
  type = string
}

variable "ladder_unk_0" {
  type = list(string)
}

variable "ladder_unk_1" {
  type = list(string)
}

variable "ladder_val_0" {
  type = list(string)
}

variable "ladder_val_1" {
  type = list(string)
}

variable "ladder_val_2" {
  type = string
}

resource "terraform_data" "ladder_u_0" {
  input = var.ladder_sample_0
}

resource "terraform_data" "ladder_ue_0" {
  input = var.ladder_esample_0
}

resource "terraform_data" "ladder_u_1" {
  input = var.ladder_sample_1
}

resource "terraform_data" "ladder_ue_1" {
  input = var.ladder_esample_1
}

resource "terraform_data" "ladder_u_2" {
  input = var.ladder_sample_2
}

locals {
  ladder_in_0 = var.ladder_mode_0 == "unknown" ? terraform_data.ladder_u_0.output : var.ladder_mode_0 == "known" ? tolist([for k, v in var.ladder_val_0 : contains(var.ladder_unk_0, tostring(k)) ? terraform_data.ladder_ue_0.output : v]) : null
  ladder_in_1 = var.ladder_mode_1 == "unknown" ? terraform_data.ladder_u_1.output : var.ladder_mode_1 == "known" ? tolist([for k, v in var.ladder_val_1 : contains(var.ladder_unk_1, tostring(k)) ? terraform_data.ladder_ue_1.output : v]) : null
  ladder_in_2 = var.ladder_mode_2 == "unknown" ? terraform_data.ladder_u_2.output : var.ladder_mode_2 == "known" ? var.ladder_val_2 : null
}

output "canary_ladder_u_0" {
  value = terraform_data.ladder_u_0.output
}

output "canary_ladder_ue_0" {
  value = terraform_data.ladder_ue_0.output
}

output "canary_ladder_u_1" {
  value = terraform_data.ladder_u_1.output
}

output "canary_ladder_ue_1" {
  value = terraform_data.ladder_ue_1.output
}

output "canary_ladder_u_2" {
  value = terraform_data.ladder_u_2.output
}

output "canary_known" {
  value = "known"
}

