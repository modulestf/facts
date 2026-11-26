locals {
  role = "arn:aws:iam::111111111111:role/admin"
}
provider "aws" {
  assume_role {
    role_arn = local.role
  }
}
