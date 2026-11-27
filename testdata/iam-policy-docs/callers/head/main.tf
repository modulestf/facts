module "docs" {
  source = "./modules/docs"
}
output "all" {
  value = module.docs
}
