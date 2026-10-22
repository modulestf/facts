output "widget_name" {
  description = "Name of the widget"
  value       = aws_demo_widget.this[0].name
}

output "name" {
  description = "Name passed in"
  value       = var.name
}

output "widget_id" {
  description = "ID of the widget"
  value       = { id = try(aws_demo_widget.this[0].id, aws_demo_widget.other[0].id, null) }
}
