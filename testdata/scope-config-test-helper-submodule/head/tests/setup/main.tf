terraform {
  required_providers {
    helm = {
      source = "hashicorp/helm"
    }
  }
}

module "fixtures" {
  source = "git::https://example.com/fixtures.git"
}
