data "aws_partition" "current" {}

resource "aws_demo_widget" "this" {
  name = var.name
  arn  = "arn:${data.aws_partition.current.partition}:demo"
}
