package behaviour

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// reasonNoop is the reason of a trigger the sweep confirms a proof for.
const reasonNoop = "the head is the base for every caller that does not already pass the name of an added toggle: " +
	"each keeps its default false, so coalesce skips the argument it guards, which reads nothing the base does not; " +
	"values from the environment, var files outside the tree and Terragrunt inputs outside the tree cannot be seen; " +
	"the cases pin the toggles to false"

// reasonSmoke is added when the cases of a proven trigger are past the cap.
const reasonSmoke = "past the cap the cases are every input off and each turned on alone, a smoke check of the proof, not coverage"

// addedToggles are the variables of dir the head adds that no module call
// at the base can set: optional, as rule A asks, and defaulting to false.
// A directory with a file the walk does not read (.tf.json, .tofu,
// .tofu.json, which may declare or override a variable), or a variable
// definitions file at or below it, at either revision, has none; nor has a
// name an HCL file of either tree that Terraform does not load, such as a
// test file or a Terragrunt configuration, mentions, since it may set it
// for a root.
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
			out[name] = !mentioned(c.base, name) && !mentioned(c.head, name)
		}
	}
	return out
}

// hasVarFiles reports a variable definitions file at or below dir, such as
// terraform.tfvars or env/prod.tfvars, which sets variables of a root
// module with no caller.
func hasVarFiles(t *facts.Tree, dir string) bool {
	if t == nil {
		return true
	}
	return slices.ContainsFunc(t.Paths, func(p string) bool {
		name := strings.ToLower(path.Base(p))
		return (dir == "." || strings.HasPrefix(p, dir+"/")) &&
			(strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json"))
	})
}

// mentioned reports a name as a word in an .hcl or .json file of the tree
// other than the lock file, such as x.tftest.hcl, x.tftest.json or
// terragrunt.hcl, which may set it for a root; such a file that is not a
// regular file counts.
func mentioned(t *facts.Tree, name string) bool {
	if t == nil {
		return true
	}
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_-])`)
	for _, p := range t.Paths {
		base := strings.ToLower(path.Base(p))
		if !strings.HasSuffix(base, ".hcl") && !strings.HasSuffix(base, ".json") || base == ".terraform.lock.hcl" {
			continue
		}
		if e := t.Entries[p]; e.Kind != facts.KindFile || word.Match(e.Data) {
			return true
		}
	}
	return false
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
	if len(drop) == 0 || len(drop) != reads || !readsKnown(head, drop, base, c.views[dir][1].vars) || c.callSets(dir, pins) {
		return nil
	}
	if normKey(base.expr.Range().SliceBytes(base.src), false) != tokensWithout(head, drop) {
		return nil
	}
	slices.Sort(pins)
	return pins
}

// readsKnown reports that the dropped arguments read only what resolves
// whatever the toggle and adds no dependency: a variable the directory
// declares, path.module, path.root, path.cwd, terraform.workspace, and the
// resources, data sources, module calls and locals, and count or each, the
// base expression reads. Terraform resolves every reference of an
// expression, so one that does not resolve fails the head for every caller.
func readsKnown(head *exprInfo, drop []hcl.Range, base *exprInfo, vars map[string]string) bool {
	baseRefs, baseRoots := map[string]bool{}, map[string]bool{}
	for _, tr := range base.expr.Variables() {
		baseRoots[tr.RootName()] = true
		if k, ok := refKey(tr); ok {
			baseRefs[k] = true
		}
	}
	for _, tr := range head.expr.Variables() {
		r := tr.SourceRange()
		if !slices.ContainsFunc(drop, func(d hcl.Range) bool { return d.Start.Byte <= r.Start.Byte && r.End.Byte <= d.End.Byte }) {
			continue
		}
		k, ok := refKey(tr)
		switch tr.RootName() {
		case "var":
			if name, known := varName(tr); !known || vars[name] == "" {
				return false
			}
		case "path":
			if k != "path.module" && k != "path.root" && k != "path.cwd" {
				return false
			}
		case "terraform":
			if k != "terraform.workspace" {
				return false
			}
		case "count", "each":
			if !baseRoots[tr.RootName()] {
				return false
			}
		default:
			if !ok || !baseRefs[k] {
				return false
			}
		}
	}
	return true
}

// refKey is the object a traversal reads: type.name, data.type.name,
// module.name or local.name.
func refKey(tr hcl.Traversal) (string, bool) {
	n := 2
	if tr.RootName() == "data" {
		n = 3
	}
	if len(tr) < n {
		return "", false
	}
	parts := []string{tr.RootName()}
	for _, step := range tr[1:n] {
		a, ok := step.(hcl.TraverseAttr)
		if !ok {
			return "", false
		}
		parts = append(parts, a.Name)
	}
	return strings.Join(parts, "."), true
}

// callSets reports a module call of the head whose source is a local path
// to dir and that sets one of names: an existing call the change makes
// pass the toggle.
func (c *classifier) callSets(dir string, names []string) bool {
	for d, v := range c.views {
		if v[1] == nil {
			continue
		}
		for _, it := range v[1].items {
			if it.kind != itemModule {
				continue
			}
			// A generated wrapper line passes on only what its caller passes.
			sets := func(n string) bool {
				a := it.block.Body.Attributes[n]
				return a != nil && (&exprInfo{a.Expr, it.src}).key() != normKey([]byte("try(each.value."+n+", var.defaults."+n+", false)"), false)
			}
			src := it.block.Body.Attributes["source"]
			if src == nil || !slices.ContainsFunc(names, sets) {
				continue
			}
			parts, ok := templateParts(src.Expr)
			if !ok {
				return true
			}
			s, static := literalPrefix(parts)
			if !static {
				return true
			}
			// Terraform reads a backslash in a local source as a slash.
			s = strings.ReplaceAll(s, `\`, "/")
			if (strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")) && path.Clean(path.Join(d, s)) == dir {
				return true
			}
		}
	}
	return false
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
// argument and the comma after it left out, or the comma before it for the
// last argument of a call written without a trailing comma.
func tokensWithout(head *exprInfo, drop []hcl.Range) string {
	rng := head.expr.Range()
	toks, _ := hclsyntax.LexExpression(rng.SliceBytes(head.src), "", rng.Start)
	var out []hclsyntax.Token
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
		case skipComma && t.Type == hclsyntax.TokenCParen && len(out) > 0 && out[len(out)-1].Type == hclsyntax.TokenComma:
			out = out[:len(out)-1]
		}
		skipComma = false
		out = append(out, t)
	}
	var b strings.Builder
	for _, t := range out {
		b.WriteString(t.Type.String())
		b.WriteByte(0)
		b.Write(t.Bytes)
		b.WriteByte(0)
	}
	return b.String()
}
