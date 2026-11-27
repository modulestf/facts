provider "aws" {
  assume_role {
    role_arn = file("role.txt")
  }
}
