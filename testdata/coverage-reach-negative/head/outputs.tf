output "new" {
  description = "The new widget"
  value       = aws_demo_widget.new
}

output "plain_arns" {
  description = "ARNs of the plain widget, as a list"
  value       = aws_demo_widget.plain[*].arn
}

output "plain_id" {
  description = "ID of the plain widget"
  value       = try(aws_demo_widget.plain.id, null)
}
