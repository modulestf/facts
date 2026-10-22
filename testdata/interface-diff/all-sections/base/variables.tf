variable "name" {
  description = "Name of the thing"
  type        = string
}

variable "token" {
  type    = string
  default = null
}

variable "untouched" {
  type = number
}
