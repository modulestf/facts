output "widget_id" {
  description = "ID of the widget"
  value       = try(aws_demo_widget.this[0].id, null)
}

output "extra_arn" {
  description = "ARN of the extra gadget"
  value       = try(aws_demo_gadget.extra[0].arn, null)
}

output "extra_id" {
  description = "ID of the extra gadget"
  value       = try(aws_demo_gadget.extra[0].id, null)
}
