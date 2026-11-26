resource "aws_lb" "this" {
  security_groups = compact(concat(aws_security_group.this[*].id, var.security_groups))
}

resource "aws_demo" "lookup" {
  value = lookup(var.cfg, "subnets", [])
}

resource "aws_demo" "try" {
  value = try(var.cfg.subnets, [])
}

resource "aws_demo" "merge" {
  value = merge(var.tags, {})
}

resource "aws_demo" "conditional" {
  value = var.on ? var.subnets : []
}

resource "aws_demo" "for" {
  value = [for s in var.subnets : s]
}

resource "aws_demo" "file" {
  value = file("x")
}

resource "aws_demo" "templatefile" {
  value = templatefile("x", {})
}

resource "aws_demo" "jsondecode" {
  value = jsondecode(var.s)
}

resource "aws_demo" "count_ref" {
  value = "x-${count.index}"
}

resource "aws_demo" "each_ref" {
  value = each.key
}

resource "aws_demo" "self_ref" {
  value = self.id
}

resource "aws_demo" "operator" {
  value = var.subnets + 1
}

resource "aws_demo" "parens" {
  value = (var.a)
}

resource "aws_demo" "directive" {
  value = "%{if var.a}x%{endif}"
}

resource "aws_demo" "expand" {
  value = concat(var.a...)
}

resource "aws_demo" "heredoc" {
  value = <<EOT
x
EOT
}

resource "aws_demo" "provider_func" {
  value = provider::aws::arn_parse(var.a)
}

resource "aws_demo" "negative" {
  value = -1
}

resource "aws_demo" "literals" {
  value = [true, false, null, 1.5, "s"]
}

resource "aws_demo" "object" {
  value = { a = var.a, "b" = local.b }
}

resource "aws_demo" "object_key_expr" {
  value = { (var.k) = 1 }
}

resource "aws_demo" "splat" {
  value = var.list[*].id
}

resource "aws_demo" "index" {
  value = var.map["k"][0]
}

resource "aws_demo" "interp" {
  value = "${var.a}-${local.b[0]}"
}

resource "aws_demo" "funcs" {
  value = join(",", tolist(toset(keys(tomap(var.m)))))
}
