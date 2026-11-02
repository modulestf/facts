package depth

import "strings"

// builtinFunctions are Terraform's built-in functions. A call to any other
// name, or to a provider-defined function, blocks light. Kept sorted; a
// test checks.
var builtinFunctions = []string{
	"abs", "abspath", "alltrue", "anytrue",
	"base64decode", "base64encode", "base64gzip", "base64sha256", "base64sha512", "basename", "bcrypt",
	"can", "ceil", "chomp", "chunklist", "cidrhost", "cidrnetmask", "cidrsubnet", "cidrsubnets",
	"coalesce", "coalescelist", "compact", "concat", "contains", "csvdecode",
	"dirname", "distinct", "element", "endswith", "ephemeralasnull",
	"file", "filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5", "fileset",
	"filesha1", "filesha256", "filesha512", "flatten", "floor", "format", "formatdate", "formatlist",
	"indent", "index", "issensitive", "join", "jsondecode", "jsonencode", "keys",
	"length", "log", "lookup", "lower", "matchkeys", "max", "md5", "merge", "min",
	"nonsensitive", "one", "parseint", "pathexpand", "plantimestamp", "pow",
	"range", "regex", "regexall", "replace", "reverse", "rsadecrypt",
	"sensitive", "setintersection", "setproduct", "setsubtract", "setunion", "sha1", "sha256", "sha512",
	"signum", "slice", "sort", "split", "startswith", "strcontains", "strrev", "substr", "sum",
	"templatefile", "templatestring", "textdecodebase64", "textencodebase64", "timeadd", "timecmp", "timestamp", "title",
	"tobool", "tolist", "tomap", "tonumber", "toset", "tostring", "transpose",
	"trim", "trimprefix", "trimspace", "trimsuffix", "try", "upper", "urlencode", "uuid", "uuidv5",
	"values", "yamldecode", "yamlencode", "zipmap",
}

var builtinSet = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range builtinFunctions {
		m[f] = true
	}
	return m
}()

// funcName returns the built-in name of a call, with the core:: namespace
// removed, and whether it is a built-in.
func funcName(name string) (string, bool) {
	name = strings.TrimPrefix(name, "core::")
	return name, !strings.Contains(name, "::") && builtinSet[name]
}

// Function classes of the file sink scan.
const (
	fnOther   = iota
	fnRead    // reads the file its first argument names
	fnFileset // lists files under its first argument
	fnData    // an encoding, hash, number, boolean or time: not a path
)

var funcClass = map[string]int{
	"file": fnRead, "filebase64": fnRead, "filebase64sha256": fnRead, "filebase64sha512": fnRead,
	"fileexists": fnRead, "filemd5": fnRead, "filesha1": fnRead, "filesha256": fnRead, "filesha512": fnRead,
	"templatefile": fnRead,
	"fileset":      fnFileset,
	"jsonencode":   fnData, "yamlencode": fnData, "base64encode": fnData, "base64gzip": fnData,
	"base64sha256": fnData, "base64sha512": fnData, "md5": fnData, "sha1": fnData, "sha256": fnData,
	"sha512": fnData, "bcrypt": fnData, "uuid": fnData, "uuidv5": fnData, "timestamp": fnData,
	"plantimestamp": fnData, "formatdate": fnData, "timeadd": fnData, "timecmp": fnData, "cidrhost": fnData,
	"cidrnetmask": fnData, "cidrsubnet": fnData, "cidrsubnets": fnData, "textencodebase64": fnData,
	"length": fnData, "abs": fnData, "ceil": fnData, "floor": fnData, "log": fnData, "max": fnData,
	"min": fnData, "parseint": fnData, "pow": fnData, "signum": fnData, "sum": fnData, "tonumber": fnData,
	"tobool": fnData, "alltrue": fnData, "anytrue": fnData, "can": fnData, "contains": fnData,
	"startswith": fnData, "endswith": fnData, "strcontains": fnData, "issensitive": fnData, "index": fnData,
	"range": fnData, "rsadecrypt": fnData,
}
