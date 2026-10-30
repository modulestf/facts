package behaviour

import (
	"cmp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schema"
	"github.com/zclconf/go-cty/cty"
)

// RefDetail is one contextual reference as an evaluation stands in for it.
type RefDetail struct {
	// Text is the reference as written; a splat keeps its [*].attr and a
	// dynamic index ends the text.
	Text string `json:"text"`
	// Kind is resource, data, module, local, count, each, path, terraform
	// or self; any other root name is taken for a resource type.
	Kind string `json:"kind"`
	// Type is the type constraint of the referenced attribute from the
	// provider schema, or null.
	Type *string `json:"type"`
	// Multi is count or for_each when the referenced block has one.
	Multi *string `json:"multi"`
}

var refKinds = map[string]string{
	"data": "data", "module": "module", "local": "local", "count": "count", "each": "each",
	"path": "path", "terraform": "terraform", "self": "self",
}

// refDetails lists the contextual references of the expressions of a
// trigger in dir, sorted by text and unique.
func (c *classifier) refDetails(dir string, xs ...*exprInfo) []RefDetail {
	byText := map[string]RefDetail{}
	for _, x := range xs {
		if x == nil {
			continue
		}
		roots := map[string]bool{}
		for _, tr := range x.expr.Variables() {
			roots[tr.RootName()] = true
		}
		delete(roots, "var")
		skip := map[*hclsyntax.ScopeTraversalExpr]bool{}
		_ = hclsyntax.VisitAll(x.expr, func(n hclsyntax.Node) hcl.Diagnostics {
			switch e := n.(type) {
			case *hclsyntax.SplatExpr:
				if st, ok := e.Source.(*hclsyntax.ScopeTraversalExpr); ok && roots[st.Traversal.RootName()] {
					skip[st] = true
					d := c.refDetail(dir, string(e.Range().SliceBytes(x.src)), st.Traversal, splatAttr(e))
					byText[d.Text] = d
				}
			case *hclsyntax.ScopeTraversalExpr:
				if !skip[e] && roots[e.Traversal.RootName()] {
					d := c.refDetail(dir, string(e.Range().SliceBytes(x.src)), e.Traversal, "")
					byText[d.Text] = d
				}
			}
			return nil
		})
	}
	out := make([]RefDetail, 0, len(byText))
	for _, d := range byText {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b RefDetail) int { return strings.Compare(a.Text, b.Text) })
	return out
}

// splatAttr is the first attribute a splat reads from each element.
func splatAttr(e *hclsyntax.SplatExpr) string {
	if rt, ok := e.Each.(*hclsyntax.RelativeTraversalExpr); ok {
		for _, step := range rt.Traversal {
			if n := attrName(step); n != "" {
				return n
			}
		}
	}
	return ""
}

// refDetail describes one reference. attr is the attribute a splat reads.
func (c *classifier) refDetail(dir, text string, tr hcl.Traversal, attr string) RefDetail {
	root := tr.RootName()
	d := RefDetail{Text: text, Kind: cmp.Or(refKinds[root], "resource")}
	// The block address and the attribute after it, skipping index steps.
	var names []string
	for _, step := range tr[1:] {
		if n := attrName(step); n != "" {
			names = append(names, n)
		}
	}
	var key, typ, mode string
	switch d.Kind {
	case "resource":
		if len(names) > 0 {
			key, typ, mode = "resource."+root+"."+names[0], root, itemResource
			names = names[1:]
		}
	case "data":
		if len(names) > 1 {
			key, typ, mode = "data."+names[0]+"."+names[1], names[0], itemData
			names = names[2:]
		}
	case "module":
		if len(names) > 0 {
			key = "module." + names[0]
		}
	default:
		return d
	}
	if attr == "" && len(names) > 0 && typ != "" {
		attr = names[0]
	}
	v := c.views[dir]
	for _, side := range []*view{v[1], v[0]} {
		if side == nil || key == "" || side.items[key] == nil {
			continue
		}
		b := side.items[key].block.Body
		for _, meta := range []string{"count", "for_each"} {
			if b.Attributes[meta] != nil {
				m := meta
				d.Multi = &m
			}
		}
		if attr != "" && c.schema != nil && typ != "" {
			local, _, _ := strings.Cut(typ, "_")
			if a := b.Attributes["provider"]; a != nil {
				if st, ok := a.Expr.(*hclsyntax.ScopeTraversalExpr); ok {
					local = st.Traversal.RootName()
				}
			}
			source := facts.ProviderKey(cmp.Or(side.providers[local], local))
			o, f := c.schema.Lookup(schema.Query{Source: source, DataSource: mode == itemData, Type: typ, Path: []string{attr}})
			if o == schema.Present && f.Attribute != nil && f.Attribute.AttributeType != cty.NilType {
				s := typeexpr.TypeString(f.Attribute.AttributeType)
				d.Type = &s
			}
		}
		break
	}
	return d
}
