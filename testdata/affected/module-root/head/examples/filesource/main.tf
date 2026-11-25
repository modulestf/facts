resource "aws_s3_object" "o" {
  source = "../../modules/a/main.tf"
}
resource "aws_s3_object" "p" {
  source = "../../modules/a"
}
