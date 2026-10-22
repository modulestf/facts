output "widget_id" {
  description = "ID of the widget"
  value       = try(aws_demo_widget.this[var.name].id, null)
}
