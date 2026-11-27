module "m" {
  source = "./m"
}
data "aws_iam_policy_document" "child" {
  count = module.m.n
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.child[0].json
}
