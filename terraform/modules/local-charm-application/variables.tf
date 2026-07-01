variable "app_name" {
  description = "Application name."
  type        = string
}

variable "model_name" {
  description = "Juju CLI model reference."
  type        = string
}

variable "charm_file" {
  description = "Full path to the local .charm artifact."
  type        = string
}

variable "app_image" {
  description = "OCI image reference for the app-image resource."
  type        = string
}

variable "config" {
  description = "Application config."
  type        = map(any)
  sensitive   = true
}

variable "wait_timeout" {
  description = "Juju wait-for timeout."
  type        = string
}
