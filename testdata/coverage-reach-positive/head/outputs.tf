output "new_id" {
  description = "ID of the new widget"
  value       = aws_demo_widget.new.id
}

output "new_arn" {
  description = "ARN of the new widget, read through a local"
  value       = local.new_arn
}
