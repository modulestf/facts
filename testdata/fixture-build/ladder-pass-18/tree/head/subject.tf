output "subject" {
  value = toset([for n in local.ladder_in_0 : upper(n)])
}

