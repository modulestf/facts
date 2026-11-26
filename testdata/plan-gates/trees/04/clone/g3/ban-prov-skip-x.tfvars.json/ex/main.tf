provider "aws" {
  region = "eu-west-1"

  # Make it faster by skipping something
  skip_metadata_api_check     = true
  skip_credentials_validation = false
  // one more comment
  skip_requesting_account_id  = true
  skip_region_validation      = false
}
