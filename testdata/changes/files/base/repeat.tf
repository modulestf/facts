locals {
  a = 1
}

locals {
  b = 2
}

moved {
  from = aws_sns_topic.x
  to   = aws_sns_topic.y
}

moved {
  from = aws_sns_topic.p
  to   = aws_sns_topic.q
}

terraform {
  required_version = ">= 1.5"
}

terraform {
  experiments = []
}
