output "gadget_id" {
  description = "ID of the gadget"
  value       = try(aws_demo_gadget.this[0].id, null)
}
