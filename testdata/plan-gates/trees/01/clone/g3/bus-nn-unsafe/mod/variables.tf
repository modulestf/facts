variable "bus_name" {
  type     = string
  nullable = false
  default  = "arn:aws:events:us-east-1:123456789012:event-bus/x"
}
variable "region" {
  default = null
}
