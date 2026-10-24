output "widget_id" {
  description = "ID of the widget"
  value       = try(aws_demo_widget.this[0].id, null)
}

output "widget_arn" {
  description = "ARN of the widget, null when it is not created"
  value       = try(aws_demo_widget.this[0].arn, null)
}

output "widget_arns" {
  description = "ARNs of the widget, empty when it is not created"
  value       = aws_demo_widget.this[*].arn
}

output "widget_arn_one" {
  description = "ARN of the widget, null when it is not created"
  value       = one(aws_demo_widget.this[*].arn)
}

output "widget_arn_list" {
  description = "ARNs of the widget, empty when it is not created"
  value       = [for w in aws_demo_widget.this : w.arn]
}

output "extra_arns" {
  description = "ARNs of the extra widgets, by key"
  value       = { for k, w in aws_demo_widget.each : k => w.arn }
}

output "extras" {
  description = "The extra widgets, by key"
  value       = aws_demo_widget.each
}

output "widget_arn_index" {
  description = "ARN of the widget, which fails when it is not created"
  value       = aws_demo_widget.this[0].arn
}

output "widget_arn_try" {
  description = "ARN of the widget, which fails when it is not created"
  value       = try(aws_demo_widget.this[0].arn)
}

output "extra_arns_splat" {
  description = "ARNs of the extra widgets, which fails on a map"
  value       = aws_demo_widget.each[*].arn
}

output "widget_arn_or_name" {
  description = "ARN of the widget, or its name when it is not created"
  value       = try(aws_demo_widget.this[0].arn, var.name)
}

output "widget_arn_or_tag" {
  description = "ARN of the widget, or a tag that may be missing"
  value       = try(aws_demo_widget.this[0].arn, var.tags["arn"])
}

output "widget_tag_names" {
  description = "Name tags of the widget, which fails where tags is null"
  value       = aws_demo_widget.this[*].tags.Name
}

output "extra_arns_named" {
  description = "ARNs of the named extra widgets, which fails on a null name"
  value       = [for w in aws_demo_widget.each : w.arn if w.name != ""]
}
