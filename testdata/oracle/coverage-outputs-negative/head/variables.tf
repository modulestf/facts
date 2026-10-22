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

variable "extra_name" {
  description = "Name of the extra gadget"
  type        = string
  default     = "extra"
}

variable "extra_description" {
  description = "Description of the extra gadget"
  type        = string
  default     = null
}

variable "extra_rules" {
  description = "Rules of the extra gadget; the provider needs at least one"
  type = list(object({
    port = number
  }))
  default = []
}
