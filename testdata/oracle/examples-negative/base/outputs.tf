output "widget_id" {
  description = "ID of the widget"
  value       = try(aws_demo_widget.this[0].id, null)
}

output "widget_name" {
  description = "Name of the widget"
  value       = try(aws_demo_widget.this[0].name, null)
}
