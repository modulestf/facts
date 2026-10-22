output "gadget_arn" {
  description = "ARN of the gadget"
  value       = aws_demo_gadget.this.arn
}

output "gadget_name" {
  description = "Name of the gadget"
  value       = try(aws_demo_gadget.this.name, "")
}
