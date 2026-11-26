provider "aws" {
  region = "eu-west-1"
  dynamic "profile" {
    for_each = []
    content {}
  }
}
