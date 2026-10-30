# L3: one curated resource under a real provider with fake credentials.
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.15.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "fixture-fake-access-key"
  secret_key                  = "fixture-fake-secret-key"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  skip_region_validation      = true

  endpoints {
    rds = "http://127.0.0.1:1"
    sts = "http://127.0.0.1:1"
  }
}

variable "vpc_security_group_ids" {
  type    = list(string)
  default = []
}

variable "serverless" {
  type    = bool
  default = true
}

resource "terraform_data" "sg" {
  input = "sg-0123456789abcdef0"
}

resource "aws_rds_cluster" "this" {
  provider = aws

  cluster_identifier     = "fixture"
  engine                 = "aurora-postgresql"
  master_username        = "fixture"
  master_password        = "fixture-password"
  skip_final_snapshot    = true
  vpc_security_group_ids = compact(concat([terraform_data.sg.output], var.vpc_security_group_ids))

  dynamic "serverlessv2_scaling_configuration" {
    for_each = var.serverless ? [1] : []

    content {
      min_capacity = 0.5
      max_capacity = serverlessv2_scaling_configuration.value
    }
  }

  lifecycle {
    ignore_changes = [master_password]
  }
}
