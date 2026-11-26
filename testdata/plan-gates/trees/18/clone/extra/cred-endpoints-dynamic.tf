provider "aws" {
  region = "eu-west-1"
  dynamic "endpoints" {
    for_each = []
    content {}
  }
}
