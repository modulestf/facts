data "aws_caller_identity" "current" {}

resource "aws_demo_widget" "this" {
  name          = var.name
  instance_type = var.instance_type
  owner         = data.aws_caller_identity.current.account_id
}
