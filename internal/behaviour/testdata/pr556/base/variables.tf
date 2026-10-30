variable "create" {
  description = "Whether to create the cluster"
  type        = bool
  default     = true
}

variable "name" {
  description = "Name of the cluster"
  type        = string
}

variable "engine" {
  description = "Database engine"
  type        = string
  default     = "aurora-postgresql"
}

variable "vpc_security_group_ids" {
  description = "Extra security groups for the cluster"
  type        = list(string)
  default     = []
}

variable "create_security_group" {
  description = "Whether to create a security group"
  type        = bool
  default     = true
}

variable "vpc_id" {
  description = "VPC of the security group"
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags for every resource"
  type        = map(string)
  default     = {}
}
