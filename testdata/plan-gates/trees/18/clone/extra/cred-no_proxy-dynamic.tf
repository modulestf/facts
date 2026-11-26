provider "aws" {
  region = "eu-west-1"
  dynamic "no_proxy" {
    for_each = []
    content {}
  }
}
