variable "create_model" {
  description = "Whether Terraform should create the Juju model. Set to false to deploy into an existing model."
  type        = bool
  default     = true
}

variable "model_name" {
  description = "Juju model name for the deployment."
  type        = string
  default     = "charm-registry"
}

variable "juju_model_cli" {
  description = "Optional Juju CLI model reference for the local charm bridge, for example controller:model."
  type        = string
  default     = null
}

variable "cloud_name" {
  description = "Juju cloud used when creating the model."
  type        = string
  default     = "k8s"
}

variable "cloud_region" {
  description = "Optional Juju cloud region used when creating the model."
  type        = string
  default     = null
}

variable "model_config" {
  description = "Model config applied when create_model is true."
  type        = map(string)
  default = {
    "test-mode"                   = "true"
    "automatically-retry-hooks"   = "false"
    "update-status-hook-interval" = "1m"
  }
}

variable "charm_file" {
  description = "Full path to the locally built charm-registry .charm artifact."
  type        = string
}

variable "app_image" {
  description = "OCI image reference for the charm-registry app-image resource."
  type        = string
}

variable "app_name" {
  description = "Juju application name for charm-registry."
  type        = string
  default     = "charm-registry"
}

variable "app_secret_key" {
  description = "Secret key passed to the charm-registry charm config."
  type        = string
  sensitive   = true
}

variable "admin_usernames" {
  description = "Comma-separated development admin usernames."
  type        = string
  default     = "admin"
}

variable "enable_insecure_dev_auth" {
  description = "Enable development-only bearer tokens when OIDC is not related."
  type        = bool
  default     = true
}

variable "extra_app_config" {
  description = "Additional charm-registry config values."
  type        = map(any)
  default     = {}
}

variable "gateway_class" {
  description = "GatewayClass provided by the target Canonical Kubernetes cluster."
  type        = string
  default     = "ck-gateway"
}

variable "api_hostname" {
  description = "Hostname routed to the charm-registry API ingress relation."
  type        = string
  default     = "api.charm-registry.test"
}

variable "oci_hostname" {
  description = "Hostname routed to the charm-registry OCI ingress relation."
  type        = string
  default     = "oci.charm-registry.test"
}

variable "gateway_channel" {
  description = "Charmhub channel for gateway-api-integrator."
  type        = string
  default     = "1/edge"
}

variable "ingress_configurator_channel" {
  description = "Charmhub channel for ingress-configurator."
  type        = string
  default     = "latest/edge"
}

variable "postgresql_channel" {
  description = "Charmhub channel for postgresql-k8s."
  type        = string
  default     = "14/stable"
}

variable "certificates_channel" {
  description = "Charmhub channel for self-signed-certificates."
  type        = string
  default     = "1/stable"
}

variable "wait_timeout" {
  description = "Timeout used by the local charm bridge and recommended waits."
  type        = string
  default     = "30m"
}
