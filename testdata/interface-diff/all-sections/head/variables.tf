variable "name" {
  description = "Name of the thing"
  type        = string
  nullable    = false

  validation {
    condition     = length(var.name) <= 64
    error_message = "Name is too long."
  }
}

variable "token" {
  type      = string
  default   = null
  sensitive = true
}

variable "untouched" {
  type = number
}
