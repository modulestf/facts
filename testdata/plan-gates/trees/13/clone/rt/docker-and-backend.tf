terraform {
  required_providers {
    docker = {
      source = "kreuzwerker/docker"
    }
  }
  backend "s3" {}
}
