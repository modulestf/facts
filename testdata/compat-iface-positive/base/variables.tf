variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"
}

variable "region_hint" {
  description = "Region the widget prefers"
  type        = string
  default     = null
}

variable "mode" {
  description = "Mode of the widget"
  type        = string
  default     = "standard"
}

variable "size" {
  description = "Size of the widget"
  type        = number
  default     = 1
}

variable "settings" {
  description = "Settings of the widget"
  type = object({
    label = optional(string)
  })
  default = {}
}

variable "instance_type" {
  description = "Instance type of the widget"
  type        = string
  default     = "t3.micro"
}
