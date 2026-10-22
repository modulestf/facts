variable "create" {
  description = "Controls if resources should be created"
  type        = bool
  default     = true
}

variable "bolts" {
  description = "Bolt names"
  type        = set(string)
  default     = []
}
