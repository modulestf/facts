variable "name" {
  type        = string
  description = "Name of the thing."
}

variable "rules" {
  type        = list(string)
  description = <<-EOT
    Rules of the thing.
  EOT
}

output "id" {
  description = "The ID of the thing."
  value       = try(aws_thing.this[0].id, null)
}
