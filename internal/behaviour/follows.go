package behaviour

import (
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

const (
	reasonFollowsReads = "an added optional variable: its state follows the triggers whose source holds a read of it"
	reasonFollowsNone  = "an added optional variable that no expression of its module reads"
	reasonFollowsLine  = "a generated wrapper line that passes an added optional variable through: its state follows that variable's trigger"
	reasonFollowCapped = "a trigger it would follow is past the cap, so it selects its category's levels"
)

// fileFuncs are the functions that read files: what a file holds is not
// walked, so a call of one may carry a read the walk cannot see.
var fileFuncs = map[string]bool{
	"file": true, "templatefile": true, "fileset": true, "fileexists": true, "filebase64": true,
	"filemd5": true, "filesha1": true, "filesha256": true, "filesha512": true,
	"filebase64sha256": true, "filebase64sha512": true,
}

// follow numbers the specs and links the followers: rule A, an added
// optional variable that follows the triggers holding its reads, then rule
// B, a generated wrapper line that follows the variable it passes through.
func (c *classifier) follow(specs []spec) {
	for i := range specs {
		specs[i].sid = i
	}
	accepted := map[string]int{} // dir + "\x00" + name -> sid
	for i := range specs {
		s := &specs[i]
		if s.variable == nil {
			continue
		}
		dir := path.Dir(s.file)
		follows, ok := c.ruleA(dir, s.variable, specs)
		if !ok {
			continue
		}
		s.levels, s.follows, s.reason = []string{}, follows, reasonFollowsReads
		if len(follows) == 0 {
			s.reason = reasonFollowsNone
		}
		accepted[dir+"\x00"+s.variable.block.Labels[0]] = s.sid
	}
	for i := range specs {
		s := &specs[i]
		if sid, ok := c.ruleB(s, accepted); ok {
			s.levels, s.follows, s.reason = []string{}, []int{sid}, reasonFollowsLine
		}
	}
}

// ruleA returns the sids of the specs whose head source holds a read of the
// added variable it, and false when it does not qualify: it must be optional
// with a scope-invariant default its type admits, and every read of it in
// its module directory, all of whose files were walked, must lie inside
// another spec's source.
func (c *classifier) ruleA(dir string, it *item, specs []spec) ([]int, bool) {
	v, ok := c.views[dir]
	if !ok || v[1] == nil || v[1].jsonFile || !optionalVar(it.block, it.src) {
		return nil, false
	}
	name := it.block.Labels[0]
	reads, fileCalls, ok := varReads(v[1], name)
	if !ok || len(reads) == 0 && fileCalls {
		return nil, false
	}
	follows := []int{}
	seen := map[int]bool{}
	for _, r := range reads {
		if r.inFileCall {
			return nil, false
		}
		held := false
		for _, s := range specs {
			if s.rng == nil || s.file != r.file || s.rng.Start.Byte > r.rng.Start.Byte || r.rng.End.Byte > s.rng.End.Byte {
				continue
			}
			held = true
			if !seen[s.sid] {
				seen[s.sid] = true
				follows = append(follows, s.sid)
			}
		}
		if !held {
			return nil, false
		}
	}
	return follows, true
}

// varRead is one head read of a variable.
type varRead struct {
	file       string
	rng        hcl.Range
	inFileCall bool // inside the arguments of a call of a file function
}

// varReads walks every file of a view for the reads of var.<name>. fileCalls
// is set when a file function is called anywhere; ok is false when a
// reference to var names no variable the walk can tell.
func varReads(v *view, name string) (reads []varRead, fileCalls, ok bool) {
	ok = true
	for _, f := range v.files {
		var calls []hcl.Range
		_ = hclsyntax.VisitAll(f.body, func(n hclsyntax.Node) hcl.Diagnostics {
			if fc, isCall := n.(*hclsyntax.FunctionCallExpr); isCall && fileFuncs[fc.Name] {
				calls = append(calls, fc.Range())
			}
			return nil
		})
		fileCalls = fileCalls || len(calls) > 0
		_ = hclsyntax.VisitAll(f.body, func(n hclsyntax.Node) hcl.Diagnostics {
			st, isTraversal := n.(*hclsyntax.ScopeTraversalExpr)
			if !isTraversal || st.Traversal.RootName() != "var" {
				return nil
			}
			got, known := varName(st.Traversal)
			switch {
			case !known:
				ok = false
			case got == name:
				r := varRead{file: f.path, rng: st.Range()}
				for _, c := range calls {
					r.inFileCall = r.inFileCall || c.Start.Byte <= r.rng.Start.Byte && r.rng.End.Byte <= c.End.Byte
				}
				reads = append(reads, r)
			}
			return nil
		})
	}
	return reads, fileCalls, ok
}

// varName is the variable a var traversal reads: var.x or var["x"].
func varName(tr hcl.Traversal) (string, bool) {
	if len(tr) < 2 {
		return "", false
	}
	switch s := tr[1].(type) {
	case hcl.TraverseAttr:
		return s.Name, true
	case hcl.TraverseIndex:
		if s.Key.Type() == cty.String && s.Key.IsKnown() && !s.Key.IsNull() {
			return s.Key.AsString(), true
		}
	}
	return "", false
}

// optionalVar reports a variable block with a scope-invariant default its
// type admits and nothing else that changes what a caller may pass: no
// validation or other nested block, not sensitive, not ephemeral, nullable.
func optionalVar(b *hclsyntax.Block, src []byte) bool {
	if len(b.Body.Blocks) > 0 {
		return false
	}
	ty := cty.DynamicPseudoType
	for name, a := range b.Body.Attributes {
		switch name {
		case "description":
		case "sensitive", "ephemeral":
			if !literalBool(a.Expr, false) {
				return false
			}
		case "nullable":
			if !literalBool(a.Expr, true) {
				return false
			}
		case "type":
			t, _, diags := typeexpr.TypeConstraintWithDefaults(a.Expr)
			if diags.HasErrors() {
				return false
			}
			ty = t
		case "default":
		default:
			return false
		}
	}
	a := b.Body.Attributes["default"]
	if a == nil {
		return false
	}
	val, ok := invariantDefault(a.Expr, src)
	if !ok {
		return false
	}
	_, err := convert.Convert(val, ty)
	return err == nil
}

func literalBool(e hclsyntax.Expression, want bool) bool {
	lit, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && lit.Val.Type() == cty.Bool && lit.Val.IsKnown() && !lit.Val.IsNull() && lit.Val.True() == want
}

// invariantDefault is the value of a default that reads the same in any
// scope: null, a bool or number literal, a bare string, [] or {}.
func invariantDefault(e hclsyntax.Expression, src []byte) (cty.Value, bool) {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return x.Val, x.Val.IsKnown()
	case *hclsyntax.TupleConsExpr:
		return cty.EmptyTupleVal, len(x.Exprs) == 0
	case *hclsyntax.ObjectConsExpr:
		return cty.EmptyObjectVal, len(x.Items) == 0
	case *hclsyntax.TemplateExpr:
		if _, ok := bareLiteral(x, src); ok {
			val, diags := x.Value(nil)
			return val, !diags.HasErrors() && val.IsKnown()
		}
	}
	return cty.NilVal, false
}

// ruleB returns the sid of the rule A variable an added argument of a
// generated wrapper passes through, and false when the line does not
// qualify: module "wrapper" in wrappers/main.tf or wrappers/<one>/main.tf,
// source unchanged and resolving to a module directory, for_each =
// var.items, and the argument's tokens exactly try(each.value.N,
// var.defaults.N, <the module's default>).
func (c *classifier) ruleB(s *spec, accepted map[string]int) (int, bool) {
	dir := path.Dir(s.file)
	if s.kind != KindAdded || s.addr != "module.wrapper" || s.h == nil || s.b != nil || path.Base(s.file) != "main.tf" ||
		strings.Contains(s.attr, ".") || dir != "wrappers" && path.Dir(dir) != "wrappers" {
		return 0, false
	}
	v, ok := c.views[dir]
	if !ok || v[0] == nil || v[1] == nil {
		return 0, false
	}
	bm, hm := v[0].items["module.wrapper"], v[1].items["module.wrapper"]
	if bm == nil || hm == nil || hm.file != s.file {
		return 0, false
	}
	bs, hs := bm.block.Body.Attributes["source"], hm.block.Body.Attributes["source"]
	fe := hm.block.Body.Attributes["for_each"]
	if bs == nil || hs == nil || fe == nil ||
		(&exprInfo{bs.Expr, bm.src}).key() != (&exprInfo{hs.Expr, hm.src}).key() ||
		(&exprInfo{fe.Expr, hm.src}).key() != normKey([]byte("var.items"), false) {
		return 0, false
	}
	parts, ok := templateParts(hs.Expr)
	if !ok {
		return 0, false
	}
	source, static := literalPrefix(parts)
	if !static || !strings.HasPrefix(source, "./") && !strings.HasPrefix(source, "../") {
		return 0, false
	}
	m := path.Clean(path.Join(dir, source))
	if m == ".." || strings.HasPrefix(m, "../") {
		return 0, false
	}
	n := s.attr
	sid, ok := accepted[m+"\x00"+n]
	if !ok {
		return 0, false
	}
	mv := c.views[m][1]
	def := mv.items["variable."+n].block.Body.Attributes["default"]
	src := mv.items["variable."+n].src
	want := normKey([]byte("try(each.value."+n+", var.defaults."+n+", "), false) +
		normKey(def.Expr.Range().SliceBytes(src), false) + normKey([]byte(")"), false)
	return sid, s.h.key() == want
}

// compactTriggers drops the duplicates of a sorted list, keeping the specs
// each stood for.
func compactTriggers(ts []Trigger) []Trigger {
	out := make([]Trigger, 0, len(ts))
	for _, t := range ts {
		if n := len(out); n > 0 && compareTriggers(out[n-1], t) == 0 {
			out[n-1].sids = append(out[n-1].sids, t.sids...)
			continue
		}
		out = append(out, t)
	}
	return out
}

// resolveFollows turns the followed specs of the numbered triggers into
// trigger ids. A follower one of whose followed specs is past the cap, or
// follows such a follower, selects its category's levels instead.
func resolveFollows(ts []Trigger) {
	id := map[int]string{}
	for _, t := range ts {
		for _, sid := range t.sids {
			id[sid] = t.ID
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range ts {
			t := &ts[i]
			if t.followSIDs == nil {
				continue
			}
			for _, sid := range t.followSIDs {
				if _, ok := id[sid]; !ok {
					t.followSIDs, t.Levels, t.Reason = nil, slices.Clone(levelsOf[t.Category]), new(reasonFollowCapped)
					for _, own := range t.sids {
						delete(id, own)
					}
					changed = true
					break
				}
			}
		}
	}
	for i := range ts {
		t := &ts[i]
		if t.followSIDs != nil {
			t.Follows = []string{}
			for _, sid := range t.followSIDs {
				if !slices.Contains(t.Follows, id[sid]) {
					t.Follows = append(t.Follows, id[sid])
				}
			}
			slices.Sort(t.Follows)
		}
		t.sids, t.followSIDs = nil, nil
	}
}
