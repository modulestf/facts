variable "boundary_arn" {
  description = "Permissions boundary"
  type        = string
  default     = "arn:aws-us-gov:iam::aws:policy/boundary"
}
