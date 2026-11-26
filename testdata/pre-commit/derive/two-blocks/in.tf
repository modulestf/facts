terraform {
  provider_meta "aws" {
    user_agent = ["x"]
  }
  provider_meta "awscc" {
    module_name = "m"
    k = 1
  }
}
