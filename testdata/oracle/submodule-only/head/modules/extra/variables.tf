variable "create" {
  description = "Controls if resources should be created"
  type        = bool
  default     = true
}

variable "name" {
  description = "Name of the role"
  type        = string
  default     = "extra"
}

variable "assume_role_policy" {
  description = "Trust policy of the role"
  type        = string
  default     = null
}

variable "path" {
  type    = string
  default = "/"
}
