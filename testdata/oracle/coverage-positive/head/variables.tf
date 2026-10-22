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

variable "extra_legacy_mode" {
  description = "Legacy mode of the extra widget"
  type        = bool
  default     = null
}

variable "extra_fast_path" {
  description = "Fast path of the extra widget"
  type        = bool
  default     = null
}
