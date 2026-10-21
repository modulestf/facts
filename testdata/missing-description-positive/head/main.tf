variable "name" {
  type = string
}

variable "blank" {
  type        = string
  description = "  "
}

output "id" {
  value = try(aws_thing.this[0].id, null)
}
