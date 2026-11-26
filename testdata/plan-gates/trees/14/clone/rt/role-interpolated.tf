provider "aws" {
  assume_role {
    role_arn = "arn:aws:iam::${var.account}:role/example"
  }
}
