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

variable "owner_name" {
  description = "Name of the team that owns the widget"
  type        = string
}

variable "selector" {
  description = "Selector passed to the widget"
  type        = map(string)
  default     = null
}

variable "retention_days" {
  description = "Number of days to keep widget history"
  type        = number
  default     = 30
}

variable "options" {
  description = "Options of the widget"
  type = object({
    size = optional(number, 2)
  })
  default = {}
}

variable "allowed_ids" {
  description = "IDs allowed to read the widget"
  type        = list(string)
  default     = []

  validation {
    condition     = length(var.allowed_ids) <= 5
    error_message = "At most 5 IDs are allowed, got ${length(var.allowed_ids)}."
  }
}

variable "zones" {
  description = "Zones the widget spans"
  type        = list(string)
  default     = ["a","b"]
}

variable "owner_email" {
  description = "Contact address of the owning team"
  type        = string
}
