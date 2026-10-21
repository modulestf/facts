variable "name" {
  description = "Name of the thing."
  type        = string
  default     = null
}

# Accepts a string or a list: callers pass both forms.
variable "targets" {
  description = "Targets of the thing."
  type        = any
}

variable "policy" {
  description = "Policy of the thing."
  type        = any # Any JSON-encodable value.
}

variable "tags" {
  description = "Tags for the thing."
  type        = map(string)
  default     = {}
}
