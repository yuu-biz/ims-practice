# The tool and provider versions. OpenTofu 1.13.0 and Terraform 1.17.2
# both ran these definitions.
# .terraform.lock.hcl pins the provider build; "tofu init" reads it.

terraform {
  required_version = ">= 1.13.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.70.0"
    }
  }
}

# The credentials come from the environment of whoever runs "tofu plan"
# or "tofu apply" (AWS_PROFILE and the like). Nothing here names an
# account.
provider "aws" {
  region = var.region

  default_tags {
    tags = module.ims_network.tags
  }
}
