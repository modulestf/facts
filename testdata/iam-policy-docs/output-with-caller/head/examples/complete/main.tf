module "root" {
  source = "../.."
}
resource "aws_iam_policy" "this" {
  policy = module.root.json
}
