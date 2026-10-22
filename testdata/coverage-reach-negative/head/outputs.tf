output "new" {
  description = "The new widget"
  value       = aws_demo_widget.new
}

output "counted_arns" {
  description = "ARNs of the counted widgets"
  value       = aws_demo_widget.counted[*].arn
}

output "counted_id" {
  description = "ID of the counted widget"
  value       = try(aws_demo_widget.counted[0].id, null)
}
