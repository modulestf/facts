variable "name" {
  description = "Name of the widget"
  type        = string
  default     = "demo"
}

variable "mode" {
  description = "Mode of the widget"
  type        = string
  default     = "standard"
  nullable    = false
}

variable "size" {
  description = "Size of the widget"
  type        = string
  default     = "1"
}

variable "settings" {
  description = "Settings of the widget"
  type = object({
    label = optional(string, "widget")
  })
  default = {}
}

variable "instance_type" {
  description = "Instance type of the widget"
  type        = string
  default     = "t3.small"
}

variable "subnet_ids" {
  description = "Subnets the widget uses"
  type        = list(string)
}
