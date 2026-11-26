provider "aws" {
  region = "us-east-1"
}
provider "aws" {
  alias                  = "b"
  region                 = var.r
  skip_region_validation = true
}
variable "r" {}
