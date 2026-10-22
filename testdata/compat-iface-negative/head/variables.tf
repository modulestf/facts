variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"
}

variable "zones" {
  description = "Zones the widget spans"
  type        = list(string)
  default     = ["a", "b"]
}

variable "labels" {
  description = "Labels of the widget"
  type        = map(any) # any value is accepted
  default     = {}
}

variable "owner" {
  description = "Team that owns the widget"
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
    error_message = "retention_days must be a positive number of days."
  }
}

variable "suffix" {
  description = "Suffix of the widget name"
  type        = string
  default     = ""
}

variable "extra" {
  description = "Extra input"
  default     = null
}

variable "options" {
  description = "Options of the widget"
  type = object({
    size  = optional(number)
    color = optional(string, "blue")
    shape = optional(string)
  })
  default = null
}

variable "added" {
  description = "Input the change adds"
  type        = string
  default     = "x"
}
