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

variable "legacy_mode" {
  description = "Switches the widget to the legacy behaviour"
  type        = bool
  default     = false
}

variable "owner_name" {
  description = "Team that owns the widget"
  type        = string
  default     = "platform"
}

variable "selector" {
  description = "Selector passed to the widget"
  type        = any
  default     = null
}

variable "retention_days" {
  description = "Number of days to keep widget history"
  type        = number
  default     = 7
}

variable "options" {
  description = "Options of the widget"
  type = object({
    size  = optional(number, 1)
    label = optional(string, "widget")
  })
  default = {}
}

variable "allowed_ids" {
  description = "IDs allowed to read the widget"
  type        = list(string)
  default     = []
}

variable "zones" {
  description = "Zones the widget spans"
  type        = list(string)
  default     = ["a", "b"]
}

variable "new_input" {
  description = "Input the change adds"
  type        = string
  default     = null
}
