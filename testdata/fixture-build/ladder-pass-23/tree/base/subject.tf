resource "aws_db_instance" "this" {
  allocated_storage   = 20
  engine              = "postgres"
  identifier          = "ladder-l3"
  instance_class      = "db.t3.micro"
  skip_final_snapshot = true
  username            = "ladder"
  vpc_security_group_ids = compact(concat(local.ladder_in_1, local.ladder_in_0))
}
