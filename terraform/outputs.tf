output "deployment_mode" {
  description = "Deployment mode used by this stack."
  value       = "provider-first"
}

output "model_name" {
  description = "Juju model reference used by CLI debug commands."
  value       = local.model_name
}

output "model_uuid" {
  description = "Juju model UUID used by provider-managed resources."
  value       = local.model_uuid
}

output "applications" {
  description = "Applications deployed by the stack."
  value = {
    charm_registry           = module.app.name
    gateway_api_integrator   = module.gateway.name
    api_ingress_configurator = module.api_ingress.name
    oci_ingress_configurator = module.oci_ingress.name
    postgresql               = module.postgresql.name
    certificates             = module.certificates.name
  }
}

output "ingress" {
  description = "Gateway API ingress hostnames."
  value = {
    gateway_class = var.gateway_class
    api_hostname  = var.api_hostname
    oci_hostname  = var.oci_hostname
  }
}

output "post_apply_debug_commands" {
  description = "Useful commands after terraform apply."
  value = [
    "juju status -m ${local.model_name} --relations --color=false",
    "juju wait-for application -m ${local.model_name} ${var.app_name} --timeout=${var.wait_timeout}",
    "juju debug-log -m ${local.model_name} --include ${var.app_name} --limit 200",
    "juju debug-log -m ${local.model_name} --include gateway-api-integrator --limit 200",
    "kubectl get gateway -n ${var.model_name} -o wide",
    "kubectl get httproutes -n ${var.model_name} -o wide",
    "kubectl get svc -n ${var.model_name} -o wide",
    "sudo k8s get load-balancer.cidrs",
    "curl -sk --resolve '${var.api_hostname}:443:<gateway-ip>' https://${var.api_hostname}/",
    "curl -sk --resolve '${var.oci_hostname}:443:<gateway-ip>' https://${var.oci_hostname}/v2/",
    "openssl s_client -connect <gateway-ip>:443 -servername ${var.api_hostname} </dev/null 2>&1 | head -20",
  ]
}
