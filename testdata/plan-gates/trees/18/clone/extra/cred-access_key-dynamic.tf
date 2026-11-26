provider "aws" {
  region = "eu-west-1"
  dynamic "access_key" {
    for_each = []
    content {}
  }
}
