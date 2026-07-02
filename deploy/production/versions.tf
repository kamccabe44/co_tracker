terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.82"
    }
  }

  # Recommended: store state remotely once this is more than a one-person setup.
  # backend "s3" {
  #   bucket         = "your-tfstate-bucket"
  #   key            = "co-tracker/production/terraform.tfstate"
  #   region         = "us-east-1"
  #   dynamodb_table = "your-tf-lock-table"
  #   encrypt        = true
  # }
}
