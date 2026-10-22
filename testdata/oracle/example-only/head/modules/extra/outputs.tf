output "role_arn" {
  description = "ARN of the role"
  value       = try(aws_iam_role.this[0].arn, null)
}
