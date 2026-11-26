provider "aws" {
  region = "eu-west-1"
  dynamic "http_proxy" {
    for_each = []
    content {}
  }
}
