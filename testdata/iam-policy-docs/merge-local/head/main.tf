variable "create" { default = true }
variable "attach_load_balancer_controller_policy" { default = false }
variable "source_policy_documents" { default = [] }
variable "override_policy_documents" { default = [] }
variable "permissions" { default = null }

data "aws_partition" "current" {}

locals {
  partition     = data.aws_partition.current.partition
  create_policy = var.create
  source_policy_documents = flatten(concat(
    data.aws_iam_policy_document.load_balancer_controller[*].json,
    var.source_policy_documents,
  ))
}

data "aws_service_principal" "elasticloadbalancing" {
  count        = 1
  service_name = "elasticloadbalancing"
}

data "aws_iam_policy_document" "this" {
  count = local.create_policy ? 1 : 0

  source_policy_documents   = local.source_policy_documents
  override_policy_documents = var.override_policy_documents

  dynamic "statement" {
    for_each = var.permissions != null ? var.permissions : {}
    content {
      actions = statement.value.actions
    }
  }
}

resource "aws_iam_policy" "this" {
  count  = local.create_policy ? 1 : 0
  name   = "x"
  policy = data.aws_iam_policy_document.this[0].json
}
