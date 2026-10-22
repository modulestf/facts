variable "create" {
  description = "Controls if resources should be created"
  type        = bool
  default     = true
}

variable "putin_khuylo" {
  description = "Creation gate the profile requires at the root"
  type        = bool
  default     = true
}

variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"
}

variable "tags" {
  description = "Tags to add to all resources"
  type        = map(string)
  default     = {}
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
