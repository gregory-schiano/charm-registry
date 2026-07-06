resource "juju_integration" "this" {
  model = var.model

  application {
    name     = var.application_a.name
    endpoint = var.application_a.endpoint
  }

  application {
    name     = var.application_b.name
    endpoint = var.application_b.endpoint
  }
}
