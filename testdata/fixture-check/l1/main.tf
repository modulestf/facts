# L1: the base and head wrappers of one expression, fed a typed unknown.
variable "vpc_security_group_ids" {
  description = <<-EOT
    Extra security group ids, a plain heredoc.
  EOT
  type        = list(string)
  default     = []
}

variable "tags" {
  type = object({
    name = optional(string, "fixture")
  })
  default = {}
}

# The output of a terraform_data resource is unknown until apply: a canary.
resource "terraform_data" "unknown_sg" {
  input = "sg-canary-1"
}

locals {
  base  = compact(concat([terraform_data.unknown_sg.output], var.vpc_security_group_ids))
  head  = concat([terraform_data.unknown_sg.output], var.vpc_security_group_ids)
  upper = [for id in var.vpc_security_group_ids : upper(id)]
}

resource "terraform_data" "probe" {
  for_each = toset(["base", "head"])

  input = {
    name   = each.key
    length = each.key == "base" ? length(local.base) : length(local.head)
    tag    = "${var.tags.name}-${each.value}"
  }
}

output "base" {
  value = local.base
}

output "head" {
  value = local.head
}
