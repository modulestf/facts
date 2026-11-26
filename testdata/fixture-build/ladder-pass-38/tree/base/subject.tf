resource "terraform_data" "subject" {
  input    = each.value
  for_each = toset(local.ladder_in_0)
}

