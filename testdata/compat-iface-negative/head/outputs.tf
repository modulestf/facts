output "widget_id" {
  description = "ID of the widget, once created"
  value       = try(aws_demo_widget.this.id, null)
}
