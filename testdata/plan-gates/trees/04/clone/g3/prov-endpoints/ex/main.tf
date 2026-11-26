provider "aws" {
  skip_metadata_api_check = true
  endpoints {
    sts = "http://127.0.0.1"
  }
}
