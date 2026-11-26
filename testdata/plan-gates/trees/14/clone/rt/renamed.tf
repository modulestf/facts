terraform {
  required_providers {
    foo = {
      source = "hashicorp/aws"
    }
  }
}
provider "foo" {
  endpoints {
    s3 = "http://127.0.0.1"
  }
}
