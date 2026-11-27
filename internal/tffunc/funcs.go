// Package tffunc holds the one table of Terraform's built-in functions. Each
// package that needs a list of functions takes the table, or a named filter
// over it, from here; a test refuses a second literal list anywhere else.
package tffunc

import "fmt"

// names are the built-in functions of Terraform v1.16.7, from baseFunctions
// and Scope.Functions in
// https://github.com/hashicorp/terraform/blob/v1.16.7/internal/lang/functions.go
// (read 2026-10-22). type is left out: only the console has it. Kept sorted
// and unique; a test checks.
var names = []string{
	"abs", "abspath", "alltrue", "anytrue", "base64decode", "base64encode", "base64gzip", "base64sha256",
	"base64sha512", "basename", "bcrypt", "can", "ceil", "chomp", "chunklist", "cidrhost",
	"cidrnetmask", "cidrsubnet", "cidrsubnets", "coalesce", "coalescelist", "compact", "concat", "contains",
	"convert", "csvdecode", "dirname", "distinct", "element", "endswith", "ephemeralasnull", "file",
	"filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5", "fileset", "filesha1", "filesha256",
	"filesha512", "flatten", "floor", "format", "formatdate", "formatlist", "indent", "index",
	"issensitive", "join", "jsondecode", "jsonencode", "keys", "length", "list", "log",
	"lookup", "lower", "map", "matchkeys", "max", "md5", "merge", "min",
	"nonsensitive", "one", "parseint", "pathexpand", "plantimestamp", "pow", "range", "regex",
	"regexall", "replace", "reverse", "rsadecrypt", "sensitive", "setintersection", "setproduct", "setsubtract",
	"setunion", "sha1", "sha256", "sha512", "signum", "slice", "sort", "split",
	"startswith", "strcontains", "strrev", "substr", "sum", "templatefile", "templatestring", "textdecodebase64",
	"textencodebase64", "timeadd", "timecmp", "timestamp", "title", "tobool", "tolist", "tomap",
	"tonumber", "toset", "tostring", "transpose", "trim", "trimprefix", "trimspace", "trimsuffix",
	"try", "upper", "urlencode", "uuid", "uuidv5", "values", "yamldecode", "yamlencode",
	"zipmap",
}

var all = func() map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}()

// Names returns a copy of the table, sorted.
func Names() []string { return append([]string(nil), names...) }

// IsBuiltin reports whether name is a built-in function of the table.
func IsBuiltin(name string) bool { return all[name] }

// Set returns the built-in functions without the ones named, as a new set
// the caller may change. It panics when a name to leave out is not in the
// table, so that a filter cannot drift from the table unnoticed.
func Set(without ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	for _, n := range without {
		if !all[n] {
			panic(fmt.Sprintf("tffunc: %q is not a built-in function", n))
		}
		delete(m, n)
	}
	return m
}

// Pure returns the built-in functions whose result depends on their
// arguments only. Left out: every file* function, templatefile,
// templatestring, abspath, pathexpand, dirname and basename (paths),
// timestamp and plantimestamp (the clock), uuid, uuidv5 and bcrypt
// (randomness or salts), rsadecrypt (keys), ephemeralasnull, yamldecode
// (alias expansion), and convert, list and map. Provider-defined functions
// (provider::...) are not built-ins and so never in it.
func Pure() map[string]bool {
	return Set(
		"file", "filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5",
		"filesha1", "filesha256", "filesha512", "fileset", "templatefile", "templatestring",
		"abspath", "pathexpand", "dirname", "basename",
		"timestamp", "plantimestamp", "uuid", "uuidv5", "bcrypt", "rsadecrypt", "ephemeralasnull", "yamldecode",
		"convert", "list", "map",
	)
}
