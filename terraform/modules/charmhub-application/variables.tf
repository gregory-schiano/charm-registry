variable "name" {
  description = "Application name."
  type        = string
}

variable "model_uuid" {
  description = "Juju model UUID."
  type        = string
}

variable "charm" {
  description = "Charmhub charm source."
  type = object({
    name     = string
    channel  = optional(string)
    base     = optional(string)
    revision = optional(number)
  })
}

variable "config" {
  description = "Application config."
  type        = map(any)
  default     = {}
}

variable "trust" {
  description = "Whether the charm requires Juju trust."
  type        = bool
  default     = false
}

variable "units" {
  description = "Number of units to deploy."
  type        = number
  default     = null
}

variable "resources" {
  description = "Charm resource overrides."
  type        = map(string)
  default     = {}
}

variable "storage_directives" {
  description = "Charm storage directives."
  type        = map(string)
  default     = {}
}
