module "this" {
  source = "../../"
}

output "all" {
  value = module.this
}
