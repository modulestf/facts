resource "null_resource" "this" {}

module "extra" {
  source = "git::https://example.com/extra.git"
}
