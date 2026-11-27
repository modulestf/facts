data "aws_iam_policy_document" "root" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
module "role" {
  source = "./modules/role"
  count  = var.enabled ? 1 : 0
  policy = data.aws_iam_policy_document.root.json
}
module "docs" {
  source = "./modules/docs"
}
module "remote" {
  source = "terraform-aws-modules/iam/aws"
  policy = data.aws_iam_policy_document.remote.json
}
data "aws_iam_policy_document" "remote" {
  statement {
    actions   = ["s3:ListBucket"]
    resources = ["*"]
  }
}
resource "aws_iam_role_policy" "from_child" {
  role   = "r"
  policy = module.docs.json
}
variable "enabled" { default = true }
