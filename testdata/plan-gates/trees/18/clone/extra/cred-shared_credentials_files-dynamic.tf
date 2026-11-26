provider "aws" {
  region = "eu-west-1"
  dynamic "shared_credentials_files" {
    for_each = []
    content {}
  }
}
