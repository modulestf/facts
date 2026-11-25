package fixture

import "github.com/modulestf/facts/internal/tffunc"

// pureFunctionSet are the Terraform built-in functions a fixture may call:
// each result depends on its arguments only. Left out on purpose: every
// file* function, templatefile, templatestring, abspath, pathexpand, dirname
// and basename (paths), timestamp and plantimestamp (the clock), uuid, uuidv5
// and bcrypt (randomness or salts), rsadecrypt (keys), ephemeralasnull,
// yamldecode (alias expansion), and every provider-defined function
// (provider::...). convert, list and map were never on the allowlist and
// stay off it. range, setproduct and format widths can still make a plan
// allocate much: the harness bounds memory and time.
var pureFunctionSet = tffunc.Set(
	"file", "filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5",
	"filesha1", "filesha256", "filesha512", "fileset", "templatefile", "templatestring",
	"abspath", "pathexpand", "dirname", "basename",
	"timestamp", "plantimestamp", "uuid", "uuidv5", "bcrypt", "rsadecrypt", "ephemeralasnull", "yamldecode",
	"convert", "list", "map",
)
