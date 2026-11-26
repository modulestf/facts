resource "terraform_data" "subject" {
  count = length(local.ladder_in_0) + 1
}

