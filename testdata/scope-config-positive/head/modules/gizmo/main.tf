resource "aws_iam_role" "this" {
  name = "gizmo"
}

resource "aws_demo_gizmo" "this" {
  name = "gizmo"
}

resource "aws_demo_gizmo_policy" "this" {
  gizmo = aws_demo_gizmo.this.id
}

module "remote" {
  source = "example-org/remote/aws"
}
