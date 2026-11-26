provider "aws" {
  assume_role {
    role_arn = "arn:aws:iam::123456789012:role/example"
  }
  endpoints {
    s3 = "http://127.0.0.1"
  }
}
