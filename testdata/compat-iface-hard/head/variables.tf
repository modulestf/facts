variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"

  validation {
    condition     = length(var.name) <= 32
    error_message = "At most 32 characters."
  }
}

variable "count_hint" {
  description = "Number of widgets"
  type        = string
  default     = "1"

  validation {
    condition     = can(tonumber(var.count_hint))
    error_message = "Must be a number."
  }
}

variable "rules" {
  description = "Rules of the widget"
  type = list(object({
    match = optional(object({
      priority = optional(number, 10)
    }), {})
  }))
  default = []
}

variable "tuning" {
  description = "Tuning of the widget"
  type        = any
  default     = {}
}

variable "limits" {
  description = "Limits of the widget"
  type = object({
    cpu   = number
    burst = number
  })
  default = null
}

variable "pair" {
  description = "A pair of values"
  type        = tuple([string])
  default     = null
}

variable "token" {
  description = "Token of the widget"
  type        = string
}

variable "new_name" {
  description = "Name the change renames"
  type        = string
}

variable "kept" {
  description = "A variable the change moves to another file unchanged"
  type        = string
  default     = "same"
}
