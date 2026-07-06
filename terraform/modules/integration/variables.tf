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

variable "wait_seconds" {
  description = "Maximum time to wait for both applications to exist before integrating them."
  type        = number
  default     = 600
}
