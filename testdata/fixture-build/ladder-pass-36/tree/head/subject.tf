resource "terraform_data" "subject" {
  input    = each.value
  for_each = toset([for n in local.ladder_in_0 : upper(n)])
}

