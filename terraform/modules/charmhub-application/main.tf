resource "juju_application" "this" {
  name       = var.name
  model_uuid = var.model_uuid

  charm {
    name     = var.charm.name
    channel  = try(var.charm.channel, null)
    base     = try(var.charm.base, null)
    revision = try(var.charm.revision, null)
  }

  config             = var.config
  resources          = var.resources
  storage_directives = var.storage_directives
  trust              = var.trust
  units              = var.units
}
