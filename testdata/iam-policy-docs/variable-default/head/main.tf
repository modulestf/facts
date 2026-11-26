variable "p" {
  type    = string
  default = <<-EOT
    {"Version": "2012-10-17", "Statement": [{"Effect": "Allow", "Action": "*", "Resource": "*"}]}
  EOT
}
resource "aws_iam_policy" "this" {
  policy = var.p
}
