provider "aws" {
  region = "eu-west-1"
  dynamic "secret_key" {
    for_each = []
    content {}
  }
}
