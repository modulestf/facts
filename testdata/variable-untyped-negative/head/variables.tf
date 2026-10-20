variable "name" {
  description = "Name of the thing."
  type        = string
  default     = null
}

variable "rules" {
  description = "Rules of the thing."
  type = map(object({
    value    = string
    priority = optional(number, 100)
  }))
  default = {}
}

variable "tags" {
  description = "Tags for the thing."
  type        = map(string)
  default     = {}
}
