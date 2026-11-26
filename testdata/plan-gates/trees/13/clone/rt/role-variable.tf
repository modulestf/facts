provider "aws" {
  assume_role {
    role_arn = var.role_arn
  }
}
