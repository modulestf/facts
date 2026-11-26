module "docs" {
  source = "./modules/docs"
}
resource "aws_iam_policy" "p" {
  policy = module.docs.json
