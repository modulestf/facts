variable "policy" { type = string }
resource "aws_iam_role_policy" "this" {
  role   = "r"
  policy = var.policy
}
