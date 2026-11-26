output "subject" {
  value = [for x in local.ladder_in_0 : x.a]
}

