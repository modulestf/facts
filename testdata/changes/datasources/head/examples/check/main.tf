check "health" {
  data "aws_ssoadmin_instances" "this" {}

  assert {
    condition     = true
    error_message = "x"
  }
}
