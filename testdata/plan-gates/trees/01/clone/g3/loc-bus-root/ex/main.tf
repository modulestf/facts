locals {
  bus = "default"
}
data "aws_cloudwatch_event_bus" "this" {
  name = local.bus
}
