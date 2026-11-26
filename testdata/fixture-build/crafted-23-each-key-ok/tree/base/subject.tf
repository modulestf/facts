resource "terraform_data" "subject" {
  for_each = { (var.ladder_each_key) = "v" }
  input    = toset(local.ladder_in_0)
}

