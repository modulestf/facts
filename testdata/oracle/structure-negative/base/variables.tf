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

variable "assume_role_policy" {
  description = "Trust policy of the widget role"
  type        = string
  default     = null
}

variable "legacy_input" {
  description = "Untyped input that predates the change"
  default     = null
}
