data "aws_iam_policy_document" "root" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
module "b" {
  source = "./modules/role"
  policy = data.aws_iam_policy_document.root.json
}
module "a" {
  source = "./modules/role"
  count  = 0
  policy = data.aws_iam_policy_document.root.json
}
