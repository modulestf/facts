locals {
  a = <<EOT
x
EOT 
}
provider "aws" {
  endpoints {
    sts = "http://127.0.0.1"
  }
}
locals {
  b = <<EOT
EOT
}
