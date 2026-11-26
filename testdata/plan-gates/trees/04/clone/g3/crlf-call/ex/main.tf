module "ok" {
  source = "../mod"
}

locals {
  pad = <<EOT
x
EOT
}

module "bad" {
  source   = "../mod"
  bus_name = "arn:aws:events:us-east-1:123456789012:event-bus/x"
}

locals {
  pad2 = <<EOT
EOT
}
