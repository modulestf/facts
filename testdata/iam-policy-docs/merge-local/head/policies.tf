data "aws_iam_policy_document" "load_balancer_controller" {
  count = var.create && var.attach_load_balancer_controller_policy ? 1 : 0

  statement {
    actions   = ["iam:CreateServiceLinkedRole"]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "iam:AWSServiceName"
      values   = [data.aws_service_principal.elasticloadbalancing[0].name]
    }
  }

  statement {
    actions = [
      "ec2:DescribeVpcs",
      "ec2:DescribeAccountAttributes",
    ]
    resources = ["arn:${local.partition}:ec2:*:*:security-group/*"]
  }
}
