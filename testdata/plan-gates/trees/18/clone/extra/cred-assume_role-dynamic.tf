provider "aws" {
  region = "eu-west-1"
  dynamic "assume_role" {
    for_each = []
    content {}
  }
}
