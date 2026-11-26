module "db" {
  source  = "terraform-aws-modules/rds/aws//modules/db_instance"
  version = "7.0.0"
}
