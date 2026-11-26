data "aws_cloudwatch_log_data_protection_policy_document" "d" {
  name = var.name
  statement {
    sid              = "Audit"
    data_identifiers = ["arn:aws:dataprotection::aws:data-identifier/EmailAddress"]
    operation {
      audit {
        findings_destination {}
      }
    }
  }
}
