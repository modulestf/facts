package fixture

import "github.com/modulestf/facts/internal/tffunc"

// pureFunctionSet are the Terraform built-in functions a fixture may call:
// each result depends on its arguments only (tffunc.Pure). range,
// setproduct and format widths can still make a plan allocate much: the
// harness bounds memory and time.
var pureFunctionSet = tffunc.Pure()
