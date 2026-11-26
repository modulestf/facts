provider "aws" {
  region = "eu-west-1"
  dynamic "ec2_metadata_service_endpoint" {
    for_each = []
    content {}
  }
}
