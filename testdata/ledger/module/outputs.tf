output "bucket_arn" {
  description = "ARN of the bucket"
  value       = try(aws_example_bucket.this[0].arn, null)
}

output "bucket_id" {
  description = "ID of the bucket"
  value       = try(aws_example_bucket.this[0].id, null)
}

output "region" {
  description = "Region the bucket is in"
  value       = data.aws_example_region.current.name
}
