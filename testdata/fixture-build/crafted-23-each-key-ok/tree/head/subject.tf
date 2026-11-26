resource "terraform_data" "subject" {
  for_each = { (var.ladder_each_key) = "v" }
  input    = "${each.key}-${length(local.ladder_in_0)}"
}

