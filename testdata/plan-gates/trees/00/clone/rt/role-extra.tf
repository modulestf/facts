provider "aws" {
  assume_role {
    role_arn    = "arn:aws:iam::123456789012:role/example"
    external_id = "x"
  }
}
