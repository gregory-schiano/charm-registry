terraform {
  required_version = ">= 1.5.0"

  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 2.1.1, < 3.0.0"
    }
  }
}
