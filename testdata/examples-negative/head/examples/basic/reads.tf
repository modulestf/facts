module "registry" {
  source = "terraform-aws-modules/x/aws"
}

module "thisx" {
  source = "../../"
}

output "a" {
  value = module.registry.unread
}

output "b" {
  value = module.this.id
}

output "c" {
  value = module.thisx.id
}

output "d" {
  value = notmodule.this.unread
}
