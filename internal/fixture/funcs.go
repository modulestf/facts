package fixture

// pureFunctions are the Terraform built-in functions a fixture may call:
// each result depends on its arguments only. Left out on purpose: every
// file* function, templatefile, templatestring, abspath, pathexpand, dirname
// and basename (paths), timestamp and plantimestamp (the clock), uuid, uuidv5
// and bcrypt (randomness or salts), rsadecrypt (keys), ephemeralasnull,
// yamldecode (alias expansion), and every provider-defined function
// (provider::...). range, setproduct and format widths can still make a plan
// allocate much: the harness bounds memory and time. Kept sorted; a test
// checks.
var pureFunctions = []string{
	"abs", "alltrue", "anytrue",
	"base64decode", "base64encode", "base64gzip", "base64sha256", "base64sha512",
	"can", "ceil", "chomp", "chunklist", "cidrhost", "cidrnetmask", "cidrsubnet", "cidrsubnets",
	"coalesce", "coalescelist", "compact", "concat", "contains", "csvdecode",
	"distinct", "element", "endswith", "flatten", "floor", "format", "formatdate", "formatlist",
	"indent", "index", "issensitive", "join", "jsondecode", "jsonencode", "keys",
	"length", "log", "lookup", "lower", "matchkeys", "max", "md5", "merge", "min",
	"nonsensitive", "one", "parseint", "pow", "range", "regex", "regexall", "replace", "reverse",
	"sensitive", "setintersection", "setproduct", "setsubtract", "setunion", "sha1", "sha256", "sha512",
	"signum", "slice", "sort", "split", "startswith", "strcontains", "strrev", "substr", "sum",
	"textdecodebase64", "textencodebase64", "timeadd", "timecmp", "title",
	"tobool", "tolist", "tomap", "tonumber", "toset", "tostring", "transpose",
	"trim", "trimprefix", "trimspace", "trimsuffix", "try", "upper", "urlencode",
	"values", "yamlencode", "zipmap",
}

var pureFunctionSet = set(pureFunctions...)
