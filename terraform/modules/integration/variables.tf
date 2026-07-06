variable "model" {
  description = "Juju model name."
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
