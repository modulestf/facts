package facts

import "strings"

// ProviderKey normalizes a provider source address: lower case, with the
// default registry host and namespace filled in, the way a provider schema
// file keys it.
func ProviderKey(source string) string {
	s := strings.ToLower(source)
	switch strings.Count(s, "/") {
	case 0:
		return "registry.terraform.io/hashicorp/" + s
	case 1:
		return "registry.terraform.io/" + s
	}
	return s
}

// IsTypeLabel reports whether s is a Terraform identifier, the form a
// resource or data source type label has: a letter or underscore, then
// letters, digits, underscores or hyphens. A report never carries any other
// label, so a line break in one cannot reach the output. This is the ASCII
// subset of Terraform's identifier rule, which also takes Unicode letters:
// stricter fails closed, and no provider's type name is outside it.
func IsTypeLabel(s string) bool {
	for i, r := range []byte(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return s != ""
}

// ResourceProviderSource is the normalized provider source of r in m: the
// local name from r's provider argument, else its type's prefix, looked up
// in m's required_providers, else the implied hashicorp namespace. A source
// that is not a plain string is treated as not declared.
func ResourceProviderSource(m *Module, r Resource) string {
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	for _, tf := range m.Terraform {
		for _, rp := range tf.RequiredProviders {
			if rp.Name == local && rp.Source != "" {
				return ProviderKey(rp.Source)
			}
		}
	}
	return ProviderKey(local)
}
