variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"

  validation {
    condition     = length(var.name) <= 64
    error_message = "At most 64 characters."
  }
}

variable "count_hint" {
  description = "Number of widgets"
  type        = number
  default     = 1
}

variable "rules" {
  description = "Rules of the widget"
  type = list(object({
    match = optional(object({
      priority = optional(number, 1)
    }), {})
  }))
  default = []
}

variable "tuning" {
  description = "Tuning of the widget"
  type = object({
    level = optional(number, 3)
  })
  default = {}
}

variable "limits" {
  description = "Limits of the widget"
  type = object({
    cpu    = number
    memory = optional(number)
  })
  default = null
}

variable "pair" {
  description = "A pair of values"
  type        = tuple([string, number])
  default     = null
}

variable "token" {
  description = "Token of the widget"
  type        = string
  default     = null
}

variable "old_name" {
  description = "Name the change renames"
  type        = string
  default     = "demo"
}
