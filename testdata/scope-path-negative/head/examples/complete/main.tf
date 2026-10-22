provider "aws" {
  region = "eu-west-1"
}

locals {
  legacy = "us-west-2"
}

locals {
  second = "us-east-1"
  zones  = { "us-west-2" = "oregon", "ap-south-1": "mumbai" }
  arn    = "arn:aws:s3:::logs-us-west-2"
  host   = "s3.us-west-2.amazonaws.com"
  named  = "${var.prefix}-us-west-2"
}

variable "prefix" {
  type    = string
  default = "x"
}
