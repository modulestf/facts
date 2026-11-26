resource "terraform_data" "subject" {
  count = var.ladder_count_index + 1
  input = local.ladder_in_0[count.index]
}

