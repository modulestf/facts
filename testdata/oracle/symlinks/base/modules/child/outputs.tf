output "id" {
  description = "ID of the child widget"
  value       = try(aws_demo_widget.child[0].id, null)
}
