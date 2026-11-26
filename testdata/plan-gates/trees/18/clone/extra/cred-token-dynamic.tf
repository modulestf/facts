provider "aws" {
  region = "eu-west-1"
  dynamic "token" {
    for_each = []
    content {}
  }
}
