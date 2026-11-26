resource "aws_rds_cluster" "this" {
  cluster_identifier  = "ladder-l3"
  engine              = "aurora-postgresql"
  master_username     = "ladder"
  skip_final_snapshot = true
  vpc_security_group_ids = concat(local.ladder_in_1, local.ladder_in_0)
}
