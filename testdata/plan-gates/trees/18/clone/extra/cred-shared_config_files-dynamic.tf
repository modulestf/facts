provider "aws" {
  region = "eu-west-1"
  dynamic "shared_config_files" {
    for_each = []
    content {}
  }
}
