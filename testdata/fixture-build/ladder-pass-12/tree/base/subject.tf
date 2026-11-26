resource "terraform_data" "subject" {
  count = toset(local.ladder_in_0)
}

