variable "name" {
  description = "Name of the widget"
  type        = string
}

variable "zones" {
  description = "Zones the widget spans"
  type        = list(string)
  default     = ["a","b"]
}

variable "labels" {
  description = "Labels of the widget"
  type        = map(string)
  default     = {}
}

variable "owner" {
  description = "Owner of the widget"
  type        = string
  default     = "platform"
  nullable    = false
}

variable "retention_days" {
  description = "Days of history to keep"
  type        = number
  default     = 7

  validation {
    condition     = var.retention_days > 0
    error_message = "Must be positive."
  }
}

variable "suffix" {
  description = "Suffix of the widget name"
  type        = string
  default     = ""

  validation {
    condition     = length(var.suffix) < 10
    error_message = "Too long."
  }
}

variable "extra" {
  description = "Extra input"
  type        = string
  default     = null
}

variable "options" {
  description = "Options of the widget"
  type = object({
    size  = number
    color = optional(string, "blue")
  })
  default = null
}
