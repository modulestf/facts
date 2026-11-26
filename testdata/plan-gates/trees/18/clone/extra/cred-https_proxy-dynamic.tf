provider "aws" {
  region = "eu-west-1"
  dynamic "https_proxy" {
    for_each = []
    content {}
  }
}
