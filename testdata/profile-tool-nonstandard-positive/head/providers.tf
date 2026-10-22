provider "aws" {
  alias    = "by_region"
  for_each = toset(["eu-west-1"])
  region   = each.key
}
