output "id" {
  description = "ID of the log group"
  value       = try(aws_demo_widget.this.id, null)
}
