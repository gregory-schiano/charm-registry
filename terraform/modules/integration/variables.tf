variable "model_uuid" {
  description = "Juju model UUID."
  type        = string
}

variable "application_a" {
  description = "First application endpoint to integrate."
  type = object({
    name     = string
    endpoint = string
  })
}

variable "application_b" {
  description = "Second application endpoint to integrate."
  type = object({
    name     = string
    endpoint = string
  })
}
