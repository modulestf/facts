locals {
  # aws_lb.this[0].idle in a comment is no read
  negated = -aws_lb.this[0].name
  splat   = aws_lb.this[*].subnets
  tags    = aws_lb.this[0].tags_all
  string  = "aws_lb.this[0].timeout"
  interp  = "${aws_lb.this[0].arn_x}"
  other   = aws_lb.this[0].arn
  whole   = [for lb in aws_lb.whole : lb.id]
  key     = aws_lb.single["key"]
  dyn     = aws_lb.dyn[var.k].x
  numeric = aws_lb.numeric[0].arn
  owners  = data.aws_ami.this.owners
  big     = "aws_lb.this[aws_lb.this[aws_lb.this[aws_lb.this[aws_lb.this[aws_lb.this["
}
