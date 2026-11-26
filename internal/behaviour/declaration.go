package behaviour

import (
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/modulestf/facts/internal/schema"
)

// declaration reads the block of variable name in the head view of a
// module directory, v being its base and head views. It is nil when either
// view is missing, which is a directory that does not decompose, or is
// unwalked: a .tf.json, .tofu or .tofu.json file there may declare or
// override the variable, at the head its default and at the base whether
// it is new. It is also nil when the head holds no such variable.
func declaration(v [2]*view, name string) *Declaration {
	if v[0] == nil || v[1] == nil || v[0].unwalked || v[1].unwalked {
		return nil
	}
	it := v[1].items["variable."+name]
	if it == nil || it.block == nil {
		return nil
	}
	d := &Declaration{Added: v[0].items["variable."+name] == nil, Default: DefaultNone, Nullable: NullableUnset}
	attrs := it.block.Body.Attributes
	if a := attrs["default"]; a != nil {
		d.Default = DefaultOther
		if val, ok := constant(a.Expr, it.src); ok {
			d.Default = DefaultLiteral
			if val.IsNull() {
				d.Default = DefaultNull
			}
		}
	}
	if a := attrs["nullable"]; a != nil {
		d.Nullable = NullableOther
		if val, ok := constant(a.Expr, it.src); ok && !val.IsNull() && val.Type() == cty.Bool {
			d.Nullable = NullableFalse
			if val.True() {
				d.Nullable = NullableTrue
			}
		}
	}
	return d
}

// constant evaluates an expression with no reference, function call, for
// expression or splat, as Terraform reads a variable's default. It is
// false for any other expression, or one that does not evaluate to a known
// value.
func constant(e hclsyntax.Expression, src []byte) (cty.Value, bool) {
	if !isLiteral(&exprInfo{e, src}) {
		return cty.NilVal, false
	}
	val, diags := e.Value(nil)
	if diags.HasErrors() || !val.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return val, true
}

// schemaRequired looks up an argument or nested block path of a resource
// or data source in the schema given with --schema, at the head's provider
// source. A dynamic segment stands for the block it generates.
func (c *classifier) schemaRequired(ctx *resourceCtx, attr string) string {
	var p []string
	for seg := range strings.SplitSeq(attr, ".") {
		if seg != "dynamic" {
			p = append(p, seg)
		}
	}
	o, f := c.schema.Lookup(schema.Query{Source: ctx.source, DataSource: ctx.mode == itemData, Type: ctx.typ, Path: p})
	switch {
	case o != schema.Present || len(p) == 0:
		return SchemaUnknown
	case f.Attribute != nil:
		if f.Attribute.Required {
			return SchemaRequiredYes
		}
		return SchemaRequiredNo
	case f.BlockType != nil:
		if f.BlockType.MinItems > 0 {
			return SchemaRequiredYes
		}
		return SchemaRequiredNo
	}
	return SchemaUnknown
}
