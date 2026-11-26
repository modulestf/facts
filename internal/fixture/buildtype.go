package fixture

import (
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// valueType is a type constraint of the builder's grammar: string, number,
// bool and any; list, set and map of one element type; tuple of element
// types; object of attributes, each optional(T) or T.
type valueType struct {
	kind  string
	elem  *valueType  // list, set, map
	elems []valueType // tuple
	attrs []typeAttr  // object, in source order, duplicates kept
}

type typeAttr struct {
	name     string
	typ      valueType
	optional bool
}

// Limits of a type constraint: tokens, and nesting below the top type.
const (
	maxTypeTokens = 400
	maxTypeDepth  = 20
)

// typeToken is one token of the grammar; typeSpace is the whitespace it
// skips. Anything else, a quote, digit or comment included, is outside it.
var (
	typeToken = regexp.MustCompile(`\A[\t\n\v\f\r ]*(?:[A-Za-z_][A-Za-z0-9_-]*|[()\[\]{},=])`)
	typeSpace = regexp.MustCompile(`\A[\t\n\v\f\r ]*\z`)
	attrName  = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_-]*\z`)
)

// parseType reads a type constraint, or reports false for anything outside
// the grammar. The text passes the token check first; the parse is the HCL
// expression parser's, read as a type, never evaluated.
func parseType(v any) (valueType, bool) {
	s, ok := str(v)
	if !ok {
		return valueType{}, false
	}
	rest, n := s, 0
	for {
		loc := typeToken.FindStringIndex(rest)
		if loc == nil {
			break
		}
		rest, n = rest[loc[1]:], n+1
	}
	if !typeSpace.MatchString(rest) || n > maxTypeTokens || n == 0 {
		return valueType{}, false
	}
	src := []byte(s)
	e, diags := hclsyntax.ParseExpression(src, "type", hcl.InitialPos)
	if diags.HasErrors() {
		return valueType{}, false
	}
	return readType(src, e, 0)
}

func readType(src []byte, e hclsyntax.Expression, depth int) (valueType, bool) {
	if depth > maxTypeDepth {
		return valueType{}, false
	}
	switch x := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(x.Traversal) != 1 {
			return valueType{}, false
		}
		switch k := x.Traversal.RootName(); k {
		case "string", "number", "bool", "any":
			return valueType{kind: k}, true
		}
	case *hclsyntax.FunctionCallExpr:
		if len(x.Args) != 1 || x.ExpandFinal {
			return valueType{}, false
		}
		switch x.Name {
		case "list", "set", "map":
			el, ok := readType(src, x.Args[0], depth+1)
			if !ok {
				return valueType{}, false
			}
			return valueType{kind: x.Name, elem: &el}, true
		case "tuple":
			t, ok := x.Args[0].(*hclsyntax.TupleConsExpr)
			if !ok {
				return valueType{}, false
			}
			out := valueType{kind: "tuple", elems: []valueType{}}
			for _, it := range t.Exprs {
				el, ok := readType(src, it, depth+1)
				if !ok {
					return valueType{}, false
				}
				out.elems = append(out.elems, el)
			}
			return out, true
		case "object":
			return readObject(src, x.Args[0], depth)
		}
	}
	return valueType{}, false
}

func readObject(src []byte, e hclsyntax.Expression, depth int) (valueType, bool) {
	o, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return valueType{}, false
	}
	out := valueType{kind: "object", attrs: []typeAttr{}}
	for _, it := range o.Items {
		k, ok := it.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
		if !ok || k.ForceNonLiteral {
			return valueType{}, false
		}
		r := k.Range()
		name := string(src[r.Start.Byte:r.End.Byte])
		if !attrName.MatchString(name) {
			return valueType{}, false
		}
		val, optional := it.ValueExpr, false
		if f, ok := val.(*hclsyntax.FunctionCallExpr); ok && f.Name == "optional" {
			if len(f.Args) != 1 || f.ExpandFinal {
				return valueType{}, false
			}
			val, optional = f.Args[0], true
		}
		t, ok := readType(src, val, depth+1)
		if !ok {
			return valueType{}, false
		}
		out.attrs = append(out.attrs, typeAttr{name: name, typ: t, optional: optional})
	}
	return out, true
}

// hasElem reports whether the type has one element type: list, set or map.
func (t valueType) hasElem() bool { return t.elem != nil }

// String writes the type in the canonical form a variable block takes.
func (t valueType) String() string {
	var b strings.Builder
	t.write(&b)
	return b.String()
}

func (t valueType) write(b *strings.Builder) {
	switch {
	case t.elem != nil:
		b.WriteString(t.kind + "(")
		t.elem.write(b)
		b.WriteString(")")
	case t.kind == "tuple":
		b.WriteString("tuple([")
		for i, e := range t.elems {
			if i > 0 {
				b.WriteString(", ")
			}
			e.write(b)
		}
		b.WriteString("])")
	case t.kind == "object":
		b.WriteString("object({")
		for i, a := range t.attrs {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(a.name + " = ")
			if a.optional {
				b.WriteString("optional(")
				a.typ.write(b)
				b.WriteString(")")
			} else {
				a.typ.write(b)
			}
		}
		b.WriteString("})")
	default:
		b.WriteString(t.kind)
	}
}

// sample is a known value of the type: "a", 1, true, one element of a
// collection, every element of a tuple and every attribute of an object.
func (t valueType) sample() any {
	switch t.kind {
	case "string", "any":
		return "a"
	case "number":
		return 1
	case "bool":
		return true
	case "list", "set":
		return []any{t.elem.sample()}
	case "map":
		return map[string]any{"k": t.elem.sample()}
	case "tuple":
		out := make([]any, 0, len(t.elems))
		for _, e := range t.elems {
			out = append(out, e.sample())
		}
		return out
	}
	out := make(map[string]any, len(t.attrs))
	for _, a := range t.attrs {
		out[a.name] = a.typ.sample()
	}
	return out
}
