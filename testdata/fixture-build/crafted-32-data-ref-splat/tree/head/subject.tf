output "subject" {
  value = concat(local.ladder_in_1, [local.ladder_in_2], local.ladder_in_0)
}

