provider "aws" {
  region = "eu-west-1"
  dynamic "insecure" {
    for_each = []
    content {}
  }
}
