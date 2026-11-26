resource "terraform_data" "subject" {
  input    = each.value
  for_each = toset([for s in local.ladder_in_0 : upper(s)])
}

