package facts

import "github.com/hashicorp/hcl/v2/hclsyntax"

// ParseHCL parses one native-syntax HCL file under the token, nesting and
// unary operator limits, and returns its body and tokens. A panic in the
// parser is recovered and returned as an error.
func ParseHCL(name string, src []byte, lim Limits) (*hclsyntax.Body, hclsyntax.Tokens, error) {
	return parseSyntax(name, src, lim)
}
