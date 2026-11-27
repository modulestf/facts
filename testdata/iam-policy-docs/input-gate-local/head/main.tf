locals {
  n = 0
}
data "aws_iam_policy_document" "root" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
module "role" {
  source = "./modules/role"
  count  = local.n
  policy = data.aws_iam_policy_document.root.json
}
