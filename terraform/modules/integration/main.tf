resource "juju_integration" "this" {
  model_uuid = var.model_uuid

  application {
    name     = var.application_a.name
    endpoint = var.application_a.endpoint
  }

  application {
    name     = var.application_b.name
    endpoint = var.application_b.endpoint
  }
}
