data "aws_partition" "current" {}
data "aws_iam_policy" "t" {
  arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/service-role/AWSLambdaENIManagementAccess"
}
