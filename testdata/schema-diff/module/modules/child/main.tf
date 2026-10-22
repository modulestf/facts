terraform {
  required_providers {
    example = {
      source = "Acme/Example"
    }
  }
}

resource "example_widget" "this" {}
