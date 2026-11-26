resource "aws_lb" "nested" {
  name = var.name
  access_logs {
    enabled = false
  }
}

resource "aws_lb" "dynamic" {
  dynamic "access_logs" {
    for_each = [1]
    content {}
  }
  name = var.name
}

resource "aws_lb" "added_attr" {
  name = var.name
  idle_timeout = 60
}

resource "aws_lb" "comment" {
  name    = var.name
  # note
  subnets = var.subnets
}

resource "aws_lb" "block_comment" {
  /* note */
  name = var.name
}

resource "aws_lb" "lifecycle" {
  name = var.name
  lifecycle {
    ignore_changes = [name]
  }
}

resource "aws_lb" "meta" {
  count = 1
  name  = var.name
}

resource "aws_lb" "two" {
  name            = var.lb_name
  security_groups = var.security_groups
}

resource "aws_lb" "trailing" {
  name = var.name # note
}

resource "aws_lb" "meta_count" {
  count = var.n_b
  name  = var.name
}

resource "aws_lb" "meta_for_each" {
  for_each = var.set_b
  name     = var.name
}

resource "aws_lb" "meta_provider" {
  provider = aws.b
  name     = var.name
}

data "aws_ami" "meta_depends_on" {
  depends_on = [aws_lb.meta_count_b]
  owners     = var.owners
}

module "meta_module" {
  source = "./m"
  count  = var.n_b
}
