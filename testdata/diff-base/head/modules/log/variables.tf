variable "name" {
  description = "Name of the log group"
  type        = string
}

variable "deletion_protection_enabled" {
  description = "Whether deletion protection is enabled"
  type        = bool
  default     = null
}
