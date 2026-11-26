module "m" {
  source = "./m"
}
data "aws_iam_policy_document" "child" {
  for_each = module.m.keys
  statement {
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${each.key}/*"]
  }
}
