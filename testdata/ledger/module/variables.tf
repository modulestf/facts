variable "create" {
  description = "Whether to create the bucket"
  type        = bool
  default     = true
}

variable "name" {
  description = "Name of the bucket"
  type        = string
}

variable "tags" {
  description = "Tags of the bucket"
  type        = map(string)
  default     = {}
}

variable "logging" {
  description = "Logging configuration of the bucket"
  type = object({
    target = string
  })
  default = null
}

variable "replica" {
  description = "Whether to create a replica bucket"
  type        = bool
  default     = false
}
