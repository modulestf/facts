output "alias_name" {
  value = resource.aws_lb.alias_name.name
}

output "alias_whole" {
  value = resource.aws_lb.alias_whole
}

locals {
  keyed   = local.m[aws_lb.keyed].name
  in_each = local.list[*].y[aws_lb.in_each]
}
