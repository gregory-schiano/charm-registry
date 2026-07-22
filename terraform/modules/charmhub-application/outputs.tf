output "name" {
  description = "Application name."
  value       = juju_application.this.name
}

output "id" {
  description = "Provider resource ID."
  value       = juju_application.this.id
}
