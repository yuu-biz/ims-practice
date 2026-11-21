# The tool and provider versions. OpenTofu 1.13.0 ran these definitions.
# .terraform.lock.hcl pins the provider build; "tofu init" reads it.

terraform {
  required_version = ">= 1.13.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 5.11.0"
    }
  }
}

# The subscription and the credentials come from the environment of
# whoever runs "tofu plan" or "tofu apply" (ARM_SUBSCRIPTION_ID after
# "az login", and the like). Nothing here names a subscription.
provider "azurerm" {
  features {}
}
