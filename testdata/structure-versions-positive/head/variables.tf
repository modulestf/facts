variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"
}

variable "child_size" {
  description = "Size of the child widget"
  type        = number
  default     = 10
}

variable "child_settings" {
  description = "Settings of the child widget"
  type = object({
    mode = optional(string, "fast")
  })
  default = {}
}
