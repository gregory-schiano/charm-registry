terraform {
  required_version = ">= 1.5.0"

  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 0.23.1, < 1.0.0"
    }
  }
}
