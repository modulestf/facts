module "ok" {
  source = "../mod"
  x = <<EOT
y
EOT
  bus_name = "arn:aws:events:us-east-1:123456789012:event-bus/x"
  region   = "us-east-1.attacker.example"
}

locals {
  pad2 = <<EOT
EOT
}
