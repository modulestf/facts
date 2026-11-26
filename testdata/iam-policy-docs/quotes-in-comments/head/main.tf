# it's a "quoted" comment with /* inside
// another "one
/* a block comment with a " quote
   and data "aws_iam_policy_document" "fake" { statement { actions = ["*"] } }
*/
data "aws_iam_policy_document" "widget" {
  statement {
    sid       = "Self" # "trailing
    actions   = ["s3:GetObject"] // "x
    resources = ["arn:aws:s3:::bucket/$${aws:username}/*", "arn:aws:s3:::b/*"]
  }
}
resource "aws_iam_policy" "this" {
  policy = data.aws_iam_policy_document.widget.json
}
