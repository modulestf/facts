package behaviour

import (
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// reasonNoop is the reason of a trigger the sweep confirms a proof for.
const reasonNoop = "the head is the base for every caller that exists at the base: each added toggle keeps its default false, " +
	"so coalesce skips the argument it guards; the cases confirm it with the toggles pinned"

// reasonSmoke is added when the cases of a proven trigger are past the cap.
const reasonSmoke = "past the cap the cases are one state at a time, a smoke check of the proof, not coverage"

// addedToggles are the variables of dir the head adds that no caller at
// the base can set: optional, as rule A asks, and defaulting to false. A
// directory with a file the walk does not read (.tf.json, .tofu,
// .tofu.json, which may declare or override a variable) or a variable
// definitions file at either revision has none.
func (c *classifier) addedToggles(dir string) map[string]bool {
	v := c.views[dir]
	if v[0] == nil || v[1] == nil || v[0].unwalked || v[1].unwalked || hasVarFiles(c.base, dir) || hasVarFiles(c.head, dir) {
		return nil
	}
	out := map[string]bool{}
	for _, it := range v[1].items {
		if it.kind != itemVariable {
			continue
		}
		name := it.block.Labels[0]
		def, ok := v[1].defaults[name]
		if _, declared := v[0].vars[name]; !declared && ok && string(def.Value) == "false" && optionalVar(it.block, it.src) {
			out[name] = true
		}
	}
	return out
}

// hasVarFiles reports a variable definitions file in dir, such as
// terraform.tfvars or x.auto.tfvars.json, which sets variables of a root
// module with no caller.
func hasVarFiles(t *facts.Tree, dir string) bool {
	if t == nil {
		return true
	}
	return slices.ContainsFunc(t.DirEntries(dir), func(e *facts.Entry) bool {
		name := strings.ToLower(path.Base(e.Path))
		return strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json")
	})
}

// noopPins returns the added toggles of dir that the head expression reads
// when it is the base expression for every caller that exists at the base,
// or nil. That holds when the head with every added toggle at its default
// false is the base: each read of a toggle is the condition of a
// coalesce argument `var.<toggle> ? <string template> : null`, which is
// then a null coalesce skips, beside another argument of string type, so
// the arguments unify to the same type; and the head without those
// arguments is the base, token for token.
func (c *classifier) noopPins(dir string, base, head *exprInfo) []string {
	if base == nil || head == nil {
		return nil
	}
	toggles := c.addedToggles(dir)
	if len(toggles) == 0 {
		return nil
	}
	reads := 0
	_ = hclsyntax.VisitAll(head.expr, func(n hclsyntax.Node) hcl.Diagnostics {
		// A for expression that names its iterator var shadows the variables.
		if f, ok := n.(*hclsyntax.ForExpr); ok && (f.KeyVar == "var" || f.ValVar == "var") {
			reads = -1
		}
		if reads < 0 {
			return nil
		}
		if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok && st.Traversal.RootName() == "var" {
			if name, known := varName(st.Traversal); !known || toggles[name] {
				reads++
			}
		}
		return nil
	})
	var drop []hcl.Range
	var pins []string
	_ = hclsyntax.VisitAll(head.expr, func(n hclsyntax.Node) hcl.Diagnostics {
		call, ok := n.(*hclsyntax.FunctionCallExpr)
		if !ok || call.Name != "coalesce" || call.ExpandFinal {
			return nil
		}
		var guarded []hcl.Range
		kept := false
		for _, a := range call.Args {
			cond, isCond := a.(*hclsyntax.ConditionalExpr)
			if !isCond {
				if _, ok := a.(*hclsyntax.TemplateExpr); ok {
					kept = true
				}
				continue
			}
			_, tmpl := cond.TrueResult.(*hclsyntax.TemplateExpr)
			name, read := toggleRead(cond.Condition)
			if tmpl && read && toggles[name] && isNullLit(cond.FalseResult) {
				guarded = append(guarded, a.Range())
				if !slices.Contains(pins, name) {
					pins = append(pins, name)
				}
				continue
			}
			_, f := cond.FalseResult.(*hclsyntax.TemplateExpr)
			kept = kept || tmpl && (f || isNullLit(cond.FalseResult))
		}
		if kept {
			drop = append(drop, guarded...)
		}
		return nil
	})
	if len(drop) == 0 || len(drop) != reads {
		return nil
	}
	if normKey(base.expr.Range().SliceBytes(base.src), false) != tokensWithout(head, drop) {
		return nil
	}
	slices.Sort(pins)
	return pins
}

// toggleRead is the variable a bare var.<name> traversal reads.
func toggleRead(n hclsyntax.Node) (string, bool) {
	st, ok := n.(*hclsyntax.ScopeTraversalExpr)
	if !ok || st.Traversal.RootName() != "var" || len(st.Traversal) != 2 {
		return "", false
	}
	return varName(st.Traversal)
}

func isNullLit(e hclsyntax.Expression) bool {
	lit, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && lit.Val.IsKnown() && lit.Val.IsNull()
}

// tokensWithout is the normKey of the head expression with each dropped
// argument and the comma after it left out.
func tokensWithout(head *exprInfo, drop []hcl.Range) string {
	rng := head.expr.Range()
	toks, _ := hclsyntax.LexExpression(rng.SliceBytes(head.src), "", rng.Start)
	var out []byte
	skipComma := false
	for _, t := range toks {
		inside := slices.ContainsFunc(drop, func(d hcl.Range) bool {
			return d.Start.Byte <= t.Range.Start.Byte && t.Range.End.Byte <= d.End.Byte
		})
		switch {
		case inside:
			skipComma = true
			continue
		case t.Type == hclsyntax.TokenComment || t.Type == hclsyntax.TokenNewline || t.Type == hclsyntax.TokenEOF:
			continue
		case skipComma && t.Type == hclsyntax.TokenComma:
			skipComma = false
			continue
		}
		skipComma = false
		out = append(out, t.Type.String()...)
		out = append(out, 0)
		out = append(out, t.Bytes...)
		out = append(out, 0)
	}
	return string(out)
}
