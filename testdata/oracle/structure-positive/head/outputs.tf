output "widget_id" {
  description = "ID of the widget"
  value       = try(aws_demo_widget.this[0].id, null)
}

output "widget_arn" {
  description = "ARN of the widget"
  value       = aws_demo_widget.this[0].arn
}
