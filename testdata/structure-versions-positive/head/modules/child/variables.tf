variable "size" {
  description = "Size of the child widget"
  type        = number
  default     = 20
}

variable "settings" {
  description = "Settings of the child widget"
  type = object({
    mode    = optional(string, "slow")
    retries = optional(number, 3)
  })
  default = {}
}
