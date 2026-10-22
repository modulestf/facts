module "this" {
  source = "../../"
}

output "g" {
  value = module.this.gone # read here too
}
