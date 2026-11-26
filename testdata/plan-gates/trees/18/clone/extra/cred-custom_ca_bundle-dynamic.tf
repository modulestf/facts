provider "aws" {
  region = "eu-west-1"
  dynamic "custom_ca_bundle" {
    for_each = []
    content {}
  }
}
