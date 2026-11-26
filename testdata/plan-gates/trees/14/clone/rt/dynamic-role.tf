provider "aws" {
  dynamic "assume_role" {
    for_each = [1]
    content {
      duration = "1h"
    }
  }
}
