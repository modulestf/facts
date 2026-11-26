provider "aws" {
  region = "eu-west-1"
  dynamic "shared_credentials_file" {
    for_each = []
    content {}
  }
}
