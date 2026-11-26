provider "aws" {
  region = "eu-west-1"
  dynamic "assume_role_with_web_identity" {
    for_each = []
    content {}
  }
}
