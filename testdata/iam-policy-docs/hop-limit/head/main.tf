data "aws_iam_policy_document" "child" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
locals {
  l0 = data.aws_iam_policy_document.child.json
  l1 = local.l0
  l2 = local.l1
  l3 = local.l2
  l4 = local.l3
  l5 = local.l4
  l6 = local.l5
  l7 = local.l6
  l8 = local.l7
  l9 = local.l8
  l10 = local.l9
  l11 = local.l10
  l12 = local.l11
  l13 = local.l12
  l14 = local.l13
  l15 = local.l14
  l16 = local.l15
  l17 = local.l16
  l18 = local.l17
  l19 = local.l18
  l20 = local.l19
  l21 = local.l20
  l22 = local.l21
  l23 = local.l22
  l24 = local.l23
  l25 = local.l24
  l26 = local.l25
  l27 = local.l26
  l28 = local.l27
  l29 = local.l28
  l30 = local.l29
  l31 = local.l30
  l32 = local.l31
  l33 = local.l32
  l34 = local.l33
  l35 = local.l34
  l36 = local.l35
  l37 = local.l36
  l38 = local.l37
  l39 = local.l38
  l40 = local.l39
  l41 = local.l40
  l42 = local.l41
  l43 = local.l42
  l44 = local.l43
  l45 = local.l44
  l46 = local.l45
  l47 = local.l46
  l48 = local.l47
  l49 = local.l48
  l50 = local.l49
  l51 = local.l50
  l52 = local.l51
  l53 = local.l52
  l54 = local.l53
  l55 = local.l54
  l56 = local.l55
  l57 = local.l56
  l58 = local.l57
  l59 = local.l58
  l60 = local.l59
  l61 = local.l60
  l62 = local.l61
  l63 = local.l62
  l64 = local.l63
  l65 = local.l64
  l66 = local.l65
  l67 = local.l66
  l68 = local.l67
  l69 = local.l68
}
resource "aws_iam_policy" "this" {
  policy = local.l69
}
