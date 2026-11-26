output "subject" {
  value = merge(local.ladder_in_0, {n = length(local.ladder_in_1)})
}

