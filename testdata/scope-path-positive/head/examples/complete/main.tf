provider "aws" {
  region = "us-west-2"
}

locals {
  backup_region = var.dr ? "ap-southeast-1" : "eu-west-1"
}

variable "dr" {
  type    = bool
  default = false
}
