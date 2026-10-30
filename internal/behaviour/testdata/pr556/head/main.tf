locals {
  create = var.create
}

resource "aws_rds_cluster" "this" {
  count = local.create ? 1 : 0

  cluster_identifier     = var.name
  engine                 = var.engine
  vpc_security_group_ids = concat(aws_security_group.this[*].id, var.vpc_security_group_ids)

  tags = var.tags
}

resource "aws_security_group" "this" {
  count = local.create && var.create_security_group ? 1 : 0

  name_prefix = "${var.name}-"
  vpc_id      = var.vpc_id
}
