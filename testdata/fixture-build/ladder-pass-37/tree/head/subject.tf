resource "terraform_data" "subject" {
  count = var.ladder_count_index + 1
  input = count.index
}

