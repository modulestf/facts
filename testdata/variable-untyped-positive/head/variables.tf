variable "name" {
  description = "Name of the thing."
}

variable "settings" {
  description = "Settings of the thing."
  type        = any
}

variable "rules" {
  description = "Rules of the thing."
  type        = map(object({ value = optional(any) }))
}
