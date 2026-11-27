locals { c = "${ /* " */ 1 }" } # "
data "aws_iam_policy_document" "widget" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
locals { e = "${ /* " */ 1 }" } /* " }
}
data "aws_iam_policy_document" "widget" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
# */
locals { f = "%{ if true /* " */ }x%{ endif }" }
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.widget.json
}
