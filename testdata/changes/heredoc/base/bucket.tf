resource "aws_s3_bucket_policy" "this" {
  bucket = var.bucket
  policy = <<EOT
{"Resource": "${var.arn}"}
EOT
  acl    = var.acl
}

resource "aws_s3_bucket_policy" "odd" {
  bucket = var.bucket
  policy = <<EOT
${join("", [
EOT
])}
EOT
  acl    = var.acl
}
