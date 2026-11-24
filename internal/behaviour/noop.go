package behaviour

import (
	"bytes"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// reasonNoop is the reason of a trigger the sweep confirms a proof for.
const reasonNoop = "the head is the base for every caller that does not already pass the name of an added toggle: " +
	"each keeps its default false, so coalesce skips the argument it guards, a bare string that reads nothing; " +
	"the directory is a module that a local module call of the tree uses, and no configuration, variable definitions, " +
	"test, Terragrunt, YAML or JSON file of the tree names a toggle outside it; " +
	"any other way a root receives the name, such as the environment or a file outside the tree, is not seen; " +
	"the cases pin the toggles to false"

// reasonSmoke is added when the cases of a proven trigger are past the cap.
const reasonSmoke = "past the cap the cases are every input off and each turned on alone, a smoke check of the proof, not coverage"

// addedToggles are the variables of dir the head adds that no module call
// at the base can set: optional, as rule A asks, and defaulting to false.
// A directory with a variable definitions file at or below it, at either
// revision, has none.
func (c *classifier) addedToggles(dir string) map[string]bool {
	v := c.views[dir]
	// A directory of the change with a file the walk does not read sets
	// blind, which silent refuses.
	if v[0] == nil || v[1] == nil || hasVarFiles(c.base, dir) || hasVarFiles(c.head, dir) {
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

// scannedNames are the files besides .tf files whose text may set a
// variable of a root: variable definitions, test files, Terragrunt and other
// HCL, JSON (.tf.json included), OpenTofu and YAML files.
var scannedNames = []string{".hcl", ".json", ".tfvars", ".tofu", ".yaml", ".yml"}

// wordRE matches a name as a whole identifier.
func wordRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_-])`)
}

// mentioned reports a name as a word in a scanned file of the tree other
// than the lock file, or TF_VAR_<name> in any file; an entry that is not a
// regular file with its content counts.
func mentioned(t *facts.Tree, name string) bool {
	if t == nil {
		return true
	}
	word, env := wordRE(name), []byte("TF_VAR_"+name)
	for _, p := range t.Paths {
		e := t.Entries[p]
		base := strings.ToLower(path.Base(p))
		scanned := base != ".terraform.lock.hcl" && slices.ContainsFunc(scannedNames, func(x string) bool { return strings.HasSuffix(base, x) })
		switch {
		case e.Kind == facts.KindFile && bytes.Contains(e.Data, env):
			return true
		case !scanned:
		case e.Kind != facts.KindFile || word.Match(e.Data):
			return true
		}
	}
	return false
}

// silent reports that nothing in either tree outside dir names any of the
// pinned toggles, so no existing caller can pass one: no scanned file
// (see mentioned); no .tf file of the base outside dir, where the name
// meant nothing; and no .tf file of a directory the change touches, other
// than a module call the head adds, which is a new caller, and the
// generated wrapper line of rule B, which passes on only what its caller
// passes and whose callers the base scan covers. The change touched no
// configuration file the walk could not read.
func (c *classifier) silent(dir string, pins []string) bool {
	if c.blind {
		return false
	}
	if c.mention == nil {
		c.mention = map[string]bool{}
	}
	for _, name := range pins {
		word := wordRE(name)
		if expired(c.lim) {
			return false
		}
		m, ok := c.mention[name]
		if !ok {
			m = mentioned(c.base, name) || mentioned(c.head, name)
			c.mention[name] = m
		}
		if m {
			return false
		}
		for _, p := range c.base.Paths {
			if e := c.base.Entries[p]; isConfigName(p) && path.Dir(p) != dir && (e.Kind != facts.KindFile || word.Match(e.Data)) {
				return false
			}
		}
		for d, v := range c.views {
			if d == dir {
				continue
			}
			for _, f := range v[1].files {
				e := c.head.Entries[f.path]
				if e == nil || e.Kind != facts.KindFile {
					return false
				}
				if word.Match(blank(e.Data, c.newCallerRanges(d, v, f.path, name))) {
					return false
				}
			}
		}
	}
	return true
}

// newCallerRanges are the byte ranges of a head file of directory d that
// cannot pass name to an existing caller: the module calls the head adds,
// unless a moved block renames an existing one to it, and the line rule B
// would accept for name.
func (c *classifier) newCallerRanges(d string, v [2]*view, file, name string) []hcl.Range {
	var out []hcl.Range
	for key, it := range v[1].items {
		if it.file != file || it.kind != itemModule {
			continue
		}
		if (v[0] == nil || v[0].items[key] == nil) && !movedTo(v[1], it.addr) {
			out = append(out, it.block.Range())
			continue
		}
		if a := it.block.Body.Attributes[name]; a != nil && c.wrapperLine(d, v, it, a, name) {
			out = append(out, a.SrcRange)
		}
	}
	return out
}

// movedTo reports a moved block of the view whose to names addr or one of
// its instances: the call is an existing one renamed.
func movedTo(v *view, addr string) bool {
	for _, it := range v.items {
		if it.kind != itemMoved {
			continue
		}
		to := strings.Join(strings.Fields(it.addr), "")
		if to == addr || strings.HasPrefix(to, addr+"[") || strings.HasPrefix(to, addr+".") {
			return true
		}
	}
	return false
}

// wrapperLine reports the generated wrapper line of rule B: module
// "wrapper" in wrappers/main.tf or wrappers/<one>/main.tf (a directory the
// walk cannot read sets blind), for_each = var.items, the defaults and
// items variables {} of type any at both revisions, and the line
// try(each.value.N, var.defaults.N, false).
func (c *classifier) wrapperLine(d string, v [2]*view, it *item, a *hclsyntax.Attribute, name string) bool {
	if it.key != "module.wrapper" || path.Base(it.file) != "main.tf" || d != "wrappers" && path.Dir(d) != "wrappers" ||
		v[0] == nil {
		return false
	}
	fe := it.block.Body.Attributes["for_each"]
	if fe == nil || (&exprInfo{fe.Expr, it.src}).key() != normKey([]byte("var.items"), false) {
		return false
	}
	for _, side := range v {
		for _, n := range []string{"defaults", "items"} {
			if !emptyObjectVar(side.items["variable."+n]) {
				return false
			}
		}
	}
	return (&exprInfo{a.Expr, it.src}).key() == normKey([]byte("try(each.value."+name+", var.defaults."+name+", false)"), false)
}

// blank returns data with the bytes of each range replaced by spaces.
func blank(data []byte, rs []hcl.Range) []byte {
	if len(rs) == 0 {
		return data
	}
	out := bytes.Clone(data)
	for _, r := range rs {
		for i := r.Start.Byte; i < r.End.Byte && i < len(out); i++ {
			out[i] = ' '
		}
	}
	return out
}

// calledLocally reports a module call of a head .tf file whose source is a
// local path to dir, so the directory is used as a module. The targets are
// found once per run; past the deadline nothing is called.
func (c *classifier) calledLocally(dir string) bool {
	if c.called == nil {
		c.called = map[string]bool{}
		for _, d := range slices.Sorted(maps.Keys(c.head.Dirs)) {
			if expired(c.lim) {
				c.called = map[string]bool{}
				return false
			}
			v, ok := c.views[d]
			hv := v[1]
			if !ok {
				if hv, _ = buildView(c.head, d, c.lim); hv == nil {
					continue
				}
			}
			for _, it := range hv.items {
				if it.kind == itemModule {
					if t := localSourceDir(d, it); t != "" {
						c.called[t] = true
					}
				}
			}
		}
	}
	return c.called[dir] && !expired(c.lim)
}

// localSourceDir is the directory a module call's literal local source
// names, read as Terraform reads it, backslashes as slashes, or "".
func localSourceDir(d string, it *item) string {
	src := it.block.Body.Attributes["source"]
	if src == nil {
		return ""
	}
	parts, ok := templateParts(src.Expr)
	if !ok {
		return ""
	}
	s, static := literalPrefix(parts)
	s = strings.ReplaceAll(s, `\`, "/")
	if !static || !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
		return ""
	}
	return path.Clean(path.Join(d, s))
}

// noopPins returns the added toggles of dir that the head expression reads
// when it is the base expression for every caller that does not already
// pass a toggle's name, or nil. That holds when the head with every added
// toggle at its default false is the base: each read of a toggle is the
// condition of a coalesce argument `var.<toggle> ? <string template> :
// null`, which is then a null coalesce skips, beside another argument of
// string type, so the arguments unify to the same type; the true result is
// a bare quoted string, which reads nothing; the head without them is the
// base, token for token; and dir is a module a
// local call uses, which nothing outside it can pass a toggle (see silent).
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
			// A bare quoted string reads nothing: an interpolation would add
			// its marks (sensitive, ephemeral) and its dependencies to the
			// result whatever the condition.
			_, bare := bareLiteral(cond.TrueResult, head.src)
			if tmpl && bare && read && toggles[name] && isNullLit(cond.FalseResult) {
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
	if len(drop) == 0 || len(drop) != reads || normKey(base.expr.Range().SliceBytes(base.src), false) != tokensWithout(head, drop) {
		return nil
	}
	if !c.calledLocally(dir) || !c.silent(dir, pins) {
		return nil
	}
	slices.Sort(pins)
	return pins
}

// expired reports a deadline of the limits that has passed.
func expired(l facts.Limits) bool { return !l.Deadline.IsZero() && time.Now().After(l.Deadline) }

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
