data "aws_cloudwatch_event_bus" "this" {
  name   = "default"
  region = "eu-west-1"
}
