provider "aws" {
  region = "eu-west-1"
  dynamic "role_arn" {
    for_each = []
    content {}
  }
}
