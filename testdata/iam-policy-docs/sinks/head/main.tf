variable "x" { default = "" }
provider "aws" {
  assume_role {
    role_arn = "arn:aws:iam::123456789012:role/r"
    policy   = var.x
  }
}
resource "aws_opensearch_domain" "d" {
  domain_name     = "d"
  access_policies = var.x
}
resource "aws_organizations_policy" "o" {
  name    = "o"
  content = file("${path.module}/scp.json")
}
module "remote" {
  source = "terraform-aws-modules/iam/aws"
  policy = var.x
}
