package facts

// The resource address check of the compat_res family, whose rules are in
// rules_compat_res.go.

import (
	"slices"
	"sort"
	"strconv"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// resInstances are the managed resources of a module by address, the first
// block of each address.
func resInstances(m *Module) map[string]*Resource {
	out := map[string]*Resource{}
	if m == nil {
		return out
	}
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.Mode != "resource" {
			continue // a data block has no address a moved block can name
		}
		addr := r.Type + "." + r.Name
		if out[addr] == nil {
			out[addr] = r
		}
	}
	return out
}

// repetition returns the repetition argument of a resource, "count",
// "for_each" or "", and its attribute.
func repetition(r *Resource) (string, *Attribute) {
	for _, m := range []string{"count", "for_each"} {
		if a := bodyAttr(r.Body, m); a != nil {
			return m, a
		}
	}
	return "", nil
}

// instanceKeys are the instance keys a resource can have, as address
// suffixes: "" with no repetition, "[0]" for count, `["a"]` for for_each.
// ok is false when the keys are not statically known: a reference, a
// function other than toset, or a value that is not a literal.
func instanceKeys(r *Resource) (keys []string, ok bool) {
	mode, a := repetition(r)
	if mode == "" {
		return []string{""}, true
	}
	e, ok := parseExprAt(a.Value)
	if !ok {
		return nil, false
	}
	set := map[string]bool{}
	if !collectKeys(mode, e, set) {
		return nil, false
	}
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, true
}

// collectKeys adds the keys an expression can give. Both results of a
// conditional are possible, so both add their keys.
func collectKeys(mode string, e hclsyntax.Expression, set map[string]bool) bool {
	switch x := e.(type) {
	case *hclsyntax.ConditionalExpr:
		return collectKeys(mode, x.TrueResult, set) && collectKeys(mode, x.FalseResult, set)
	case *hclsyntax.ParenthesesExpr:
		return collectKeys(mode, x.Expression, set)
	case *hclsyntax.FunctionCallExpr:
		if mode == "for_each" && x.Name == "toset" && len(x.Args) == 1 && !x.ExpandFinal {
			return collectKeys(mode, x.Args[0], set)
		}
		return false
	}
	v, ok := literalValue(e)
	if !ok || v.IsNull() {
		return false
	}
	if mode == "count" {
		if v.Type() != cty.Number {
			return false
		}
		n, acc := v.AsBigFloat().Int64()
		if acc != 0 || n < 0 || n > 1024 { // shortcut: a larger literal count is left to the confirmer
			return false
		}
		for i := range n {
			set["["+strconv.FormatInt(i, 10)+"]"] = true
		}
		return true
	}
	t := v.Type()
	switch {
	case t.IsTupleType() || t.IsListType() || t.IsSetType():
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			if ev.IsNull() || ev.Type() != cty.String {
				return false
			}
			set["["+strconv.Quote(ev.AsString())+"]"] = true
		}
		return true
	case t.IsObjectType() || t.IsMapType():
		for it := v.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			set["["+strconv.Quote(k.AsString())+"]"] = true
		}
		return true
	}
	return false
}

// implicitKey is the key Terraform moves an instance to on its own when
// count is added or removed: no key and [0] are the same instance.
func implicitKey(k string, head []string) bool {
	switch k {
	case "":
		return slices.Contains(head, "[0]")
	case "[0]":
		return slices.Contains(head, "")
	}
	return false
}

// checkResourceAddressChanged: a managed resource address at the base whose
// instances are not all at the head under the same address and key, nor
// named by a static moved block. A move into another area, such as
// module.child.<address>, is read from the head's moved blocks directly,
// whether the change touched them or not. A removed block, a moved block
// with an expression key and keys that are not static leave the address to
// the confirmer: they are candidates, never clean. A resource of a
// submodule moved to the root stays a candidate in the submodule: callers
// of the submodule itself still lose it.
func checkResourceAddressChanged(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.resource-address-changed"
	base, head := resInstances(a.Base), resInstances(a.Head)
	movedFrom := map[string]*Moved{}
	movedTo := map[string]bool{}
	for i := range a.Head.Moved {
		m := &a.Head.Moved[i]
		if m.FromAddr == "" || m.ToAddr == "" {
			continue // an expression key maps nothing the detector can name
		}
		if movedFrom[m.FromAddr] == nil {
			movedFrom[m.FromAddr] = m
		}
		movedTo[m.ToAddr] = true
	}
	removedBlock := map[string]*Removed{}
	for i := range a.Head.Removed {
		if r := &a.Head.Removed[i]; r.FromAddr != "" && removedBlock[r.FromAddr] == nil {
			removedBlock[r.FromAddr] = r
		}
	}
	// A head address the base lacks and no moved block names: the other
	// half of a rename, paired in order with the removals of its type.
	var added []*Resource
	for addr, r := range head {
		if base[addr] == nil && !movedTo[addr] {
			added = append(added, r)
		}
	}
	sort.Slice(added, func(i, j int) bool { return lessSpan(added[i].Span, added[j].Span) })

	var res Result
	addrs := make([]string, 0, len(base))
	for addr := range base {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool { return lessSpan(base[addrs[i]].Span, base[addrs[j]].Span) })
	for _, addr := range addrs {
		b, h := base[addr], head[addr]
		if h != nil && modeText(repetition(b)) == modeText(repetition(h)) {
			continue // the same address and the same repetition give the same instances
		}
		bKeys, bOK := instanceKeys(b)
		var hKeys []string
		hOK := true
		if h != nil {
			hKeys, hOK = instanceKeys(h)
		}
		lost := false // an instance with no place at the head, or keys unknown
		if movedFrom[addr] == nil {
			if !bOK || (h != nil && !hOK) {
				lost = true
			}
			for _, k := range bKeys {
				if h != nil && (slices.Contains(hKeys, k) || implicitKey(k, hKeys)) {
					continue
				}
				if movedFrom[addr+k] == nil {
					lost = true
				}
			}
		}
		if !lost {
			continue
		}
		res.Candidates = append(res.Candidates, addressCandidate(a, id, addr, b, h, removedBlock[addr], &added))
	}
	return res
}

// addressCandidate is the candidate for one base address. Its line is a
// head line the change made: the head repetition argument, the removed
// block, or the block that took its place in a rename. With none it has no
// line and names the base file.
func addressCandidate(a *Area, id, addr string, b, h *Resource, rm *Removed, added *[]*Resource) Candidate {
	ev := newResEvidence(a)
	bMode, bArg := repetition(b)
	ev.line("merge_base", b.File, b.Line)
	if bArg != nil {
		ev.line("merge_base", b.File, bArg.Range.Start.Line)
	}
	switch {
	case h != nil:
		hMode, hArg := repetition(h)
		ev.values(new(modeText(bMode, bArg)), new(modeText(hMode, hArg)))
		line := 0
		// A repetition argument on one head line that differs from the base
		// is on a changed line; any other edit has no such line.
		if hArg != nil && (bArg == nil || hArg.Value.Text != bArg.Value.Text) && hArg.Range.Start.Line == hArg.Range.End.Line {
			line = hArg.Range.Start.Line
			ev.line("head", h.File, line)
		}
		if line == 0 {
			return ev.candidate(id, b.File, 0, addr)
		}
		return ev.candidate(id, h.File, line, addr)
	case rm != nil:
		ev.line("head", rm.File, rm.Line)
		ev.values(new(addr), nil)
		return ev.candidate(id, rm.File, rm.Line, addr)
	}
	if i := slices.IndexFunc(*added, func(r *Resource) bool { return r.Type == b.Type }); i >= 0 {
		n := (*added)[i]
		*added = slices.Delete(*added, i, i+1)
		ev.line("head", n.File, n.Line)
		ev.values(new(addr), new(n.Type+"."+n.Name))
		return ev.candidate(id, n.File, n.Line, n.Type+"."+n.Name)
	}
	ev.values(new(addr), nil)
	return ev.candidate(id, b.File, 0, addr)
}

func modeText(mode string, a *Attribute) string {
	if a == nil {
		return "single"
	}
	return mode + " = " + a.Value.Text
}

func lessSpan(x, y Span) bool {
	if x.File != y.File {
		return x.File < y.File
	}
	return x.Line < y.Line
}
