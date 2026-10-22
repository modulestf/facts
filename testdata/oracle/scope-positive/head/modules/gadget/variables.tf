variable "create" {
  description = "Controls if resources should be created"
  type        = bool
  default     = true
}

variable "name" {
  description = "Name of the gadget"
  type        = string
  default     = "extra"
}
