package fixture

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	tfjson "github.com/hashicorp/terraform-json"
)

// Schemas are the provider schemas of `terraform providers schema -json`, by
// full provider source.
type Schemas map[string]*tfjson.ProviderSchema

const maxSchemaBytes = 128 << 20

// ReadSchemas reads one `terraform providers schema -json` file.
func ReadSchemas(name string) (Schemas, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSchemaBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSchemaBytes {
		return nil, fmt.Errorf("schema: over %d bytes", maxSchemaBytes)
	}
	var ps tfjson.ProviderSchemas
	// UnmarshalJSON also refuses a format_version it does not know.
	if err := ps.UnmarshalJSON(data); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	s := Schemas{}
	for source, p := range ps.Schemas {
		if p != nil {
			s[source] = p
		}
	}
	return s, nil
}

// typeBlock returns the schema block of a data source or resource type of
// the provider with local name provider, or nil.
func (c *checker) typeBlock(kind, provider, typ string) *tfjson.SchemaBlock {
	src := c.sources[provider]
	if strings.Count(src, "/") == 1 {
		src = "registry.terraform.io/" + src
	}
	p := c.schemas[src]
	if p == nil {
		return nil
	}
	types := p.ResourceSchemas
	if kind == "data" {
		types = p.DataSourceSchemas
	}
	if t := types[typ]; t != nil {
		return t.Block
	}
	return nil
}

// covers reports a literal object that gives a non-null value to every
// computed attribute of b, nested attributes and blocks included: a mock
// then generates no value of its own. A nested block with a computed
// attribute needs its key, with at least one element for a list, set or map.
func covers(b *tfjson.SchemaBlock, e hclsyntax.Expression) bool {
	obj, ok := objectItems(e)
	if !ok || !coversAttrs(b.Attributes, obj) {
		return false
	}
	for name, bt := range b.NestedBlocks {
		if bt.Block == nil || !hasComputed(bt.Block) {
			continue
		}
		v, ok := obj[name]
		if !ok || !coversNested(bt.NestingMode, v, func(e hclsyntax.Expression) bool { return covers(bt.Block, e) }) {
			return false
		}
	}
	return true
}

func coversAttrs(attrs map[string]*tfjson.SchemaAttribute, obj map[string]hclsyntax.Expression) bool {
	for name, a := range attrs {
		if !a.Computed {
			continue
		}
		v, ok := obj[name]
		if !ok || isNull(v) {
			return false
		}
		if nt := a.AttributeNestedType; nt != nil && !coversNested(nt.NestingMode, v, func(e hclsyntax.Expression) bool {
			o, ok := objectItems(e)
			return ok && coversAttrs(nt.Attributes, o)
		}) {
			return false
		}
	}
	return true
}

// coversNested applies each to the one object of a single or group value, or
// to each of at least one element of a list, set or map value.
func coversNested(mode tfjson.SchemaNestingMode, v hclsyntax.Expression, each func(hclsyntax.Expression) bool) bool {
	var elems []hclsyntax.Expression
	switch mode {
	case tfjson.SchemaNestingModeSingle, tfjson.SchemaNestingModeGroup:
		return each(v)
	case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
		t, ok := v.(*hclsyntax.TupleConsExpr)
		if !ok {
			return false
		}
		elems = t.Exprs
	case tfjson.SchemaNestingModeMap:
		o, ok := objectItems(v)
		if !ok {
			return false
		}
		for _, e := range o {
			elems = append(elems, e)
		}
	default:
		return false
	}
	for _, e := range elems {
		if !each(e) {
			return false
		}
	}
	return len(elems) > 0
}

func hasComputed(b *tfjson.SchemaBlock) bool {
	for _, a := range b.Attributes {
		if a.Computed {
			return true
		}
	}
	for _, bt := range b.NestedBlocks {
		if bt.Block != nil && hasComputed(bt.Block) {
			return true
		}
	}
	return false
}

// objectItems returns the items of a literal object by key.
func objectItems(e hclsyntax.Expression) (map[string]hclsyntax.Expression, bool) {
	o, ok := e.(*hclsyntax.ObjectConsExpr)
	if !ok || !isLiteral(o) {
		return nil, false
	}
	items := map[string]hclsyntax.Expression{}
	for _, item := range o.Items {
		k := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr) // isLiteral checked it
		var key string
		if t, ok := k.Wrapped.(*hclsyntax.ScopeTraversalExpr); ok {
			key = t.Traversal.RootName()
		} else if key, ok = literalString(k.Wrapped); !ok {
			return nil, false
		}
		items[key] = item.ValueExpr
	}
	return items, true
}

func isNull(e hclsyntax.Expression) bool {
	l, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && l.Val.IsNull()
}
