variable "create" {
  description = "Controls if resources should be created"
  type        = bool
  default     = true
}

variable "size" {
  description = "Size of the child widget"
  type        = number
  default     = 20
}

variable "label" {
  description = "Label of the child widget"
  type        = string
  default     = "child"
  nullable    = false
}

variable "settings" {
  description = "Settings of the child widget"
  type = object({
    mode    = optional(string, "slow")
    retries = optional(number, 3)
  })
  default = {}
}
