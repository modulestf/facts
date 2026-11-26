data "aws_ssoadmin_instances" "this" {}

data "aws_identitystore_group" "g" {
  identity_store_id = "x"
}
