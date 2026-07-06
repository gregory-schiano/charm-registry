resource "juju_model" "this" {
  count = var.create_model ? 1 : 0

  name = var.model_name

  cloud {
    name   = var.cloud_name
    region = var.cloud_region
  }

  config = var.model_config
}

data "juju_model" "this" {
  count = var.create_model ? 0 : 1

  name = var.model_name
}

locals {
  model_uuid = var.create_model ? juju_model.this[0].uuid : data.juju_model.this[0].uuid
  model_name = var.model_name

  app_config = merge(
    {
      "admin-usernames"          = var.admin_usernames
      "app-secret-key"           = var.app_secret_key
      "enable-insecure-dev-auth" = var.enable_insecure_dev_auth
    },
    var.extra_app_config,
  )

  app_resources = var.app_image == null ? {} : {
    "app-image" = var.app_image
  }
}

module "gateway" {
  source = "./modules/charmhub-application"

  name       = "gateway-api-integrator"
  model_uuid = local.model_uuid
  charm = {
    name    = "gateway-api-integrator"
    channel = var.gateway_channel
  }
  config = {
    "gateway-class" = var.gateway_class
  }
  trust = true
}

module "api_ingress" {
  source = "./modules/charmhub-application"

  name       = "ingress-api"
  model_uuid = local.model_uuid
  charm = {
    name    = "ingress-configurator"
    channel = var.ingress_configurator_channel
  }
  config = {
    hostname = var.api_hostname
  }
  trust = true
}

module "oci_ingress" {
  source = "./modules/charmhub-application"

  name       = "ingress-oci"
  model_uuid = local.model_uuid
  charm = {
    name    = "ingress-configurator"
    channel = var.ingress_configurator_channel
  }
  config = {
    hostname = var.oci_hostname
  }
  trust = true

  depends_on = [module.api_ingress]
}

module "postgresql" {
  source = "./modules/charmhub-application"

  name       = "postgresql"
  model_uuid = local.model_uuid
  charm = {
    name    = "postgresql-k8s"
    channel = var.postgresql_channel
  }
  trust = true
}

module "certificates" {
  source = "./modules/charmhub-application"

  name       = "self-signed-certificates"
  model_uuid = local.model_uuid
  charm = {
    name    = "self-signed-certificates"
    channel = var.certificates_channel
  }
}

module "app" {
  source = "./modules/charmhub-application"

  name       = var.app_name
  model_uuid = local.model_uuid
  charm = {
    name     = var.app_charm_name
    channel  = var.app_channel
    revision = var.app_revision
  }
  config    = local.app_config
  resources = local.app_resources

  depends_on = [
    juju_model.this,
    data.juju_model.this,
  ]
}

module "app_api_ingress" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.app.name, endpoint = "ingress" }
  application_b = { name = module.api_ingress.name, endpoint = "ingress" }

  depends_on = [
    juju_model.this,
    data.juju_model.this,
    module.app,
    module.api_ingress,
  ]
}

module "app_oci_ingress" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.app.name, endpoint = "oci-ingress" }
  application_b = { name = module.oci_ingress.name, endpoint = "ingress" }

  depends_on = [
    juju_model.this,
    data.juju_model.this,
    module.app,
    module.oci_ingress,
  ]
}

module "app_postgresql" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.app.name, endpoint = "postgresql" }
  application_b = { name = module.postgresql.name, endpoint = "database" }

  depends_on = [
    juju_model.this,
    data.juju_model.this,
    module.app,
    module.postgresql,
  ]
}

module "gateway_certificates" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.certificates.name, endpoint = "certificates" }
  application_b = { name = module.gateway.name, endpoint = "certificates" }

  depends_on = [
    juju_model.this,
    data.juju_model.this,
    module.certificates,
    module.gateway,
  ]
}

module "api_gateway_route" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.api_ingress.name, endpoint = "gateway-route" }
  application_b = { name = module.gateway.name, endpoint = "gateway-route" }

  depends_on = [
    module.api_ingress,
    module.gateway,
  ]
}

module "oci_gateway_route" {
  source = "./modules/integration"

  model_uuid    = local.model_uuid
  application_a = { name = module.oci_ingress.name, endpoint = "gateway-route" }
  application_b = { name = module.gateway.name, endpoint = "gateway-route" }

  depends_on = [
    module.oci_ingress,
    module.gateway,
  ]
}
