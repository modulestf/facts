data "aws_cloudwatch_event_bus" "this" {
  count  = var.create ? 0 : 1
  name   = var.bus_name
  region = var.region
}
