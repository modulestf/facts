output "subject" {
  value = toset([for s in local.ladder_in_0 : upper(s)])
}

