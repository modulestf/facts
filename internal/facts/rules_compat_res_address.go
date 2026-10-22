package facts

// The resource address check of the compat_res family, whose rules are in
// rules_compat_res.go.

import (
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// repetition returns the repetition argument of a resource or module call
// body, "count", "for_each" or "", and its attribute.
func repetition(b Body) (string, *Attribute) {
	for _, m := range []string{"count", "for_each"} {
		if a := bodyAttr(b, m); a != nil {
			return m, a
		}
	}
	return "", nil
}

// instanceKeys are the instance keys a resource or module call can have, as address
// suffixes: "" with no repetition, "[0]" for count, `["a"]` for for_each.
// ok is false when the keys are not statically known: a reference, a
// function other than toset, or a value that is not a literal.
func instanceKeys(b Body) (keys []string, ok bool) {
	mode, a := repetition(b)
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

// addrBlock is a block with an address a moved block can name: a managed
// resource, <type>.<name>, or a module call, module.<name>.
type addrBlock struct {
	Span
	addr string
	kind string // the resource type, or "module"
	body Body
}

// addrBlocks are the addressable blocks of a module by address, the first
// block of each address. A data block has no address a moved block can name.
func addrBlocks(m *Module) map[string]*addrBlock {
	out := map[string]*addrBlock{}
	if m == nil {
		return out
	}
	add := func(b *addrBlock) {
		if out[b.addr] == nil {
			out[b.addr] = b
		}
	}
	for _, r := range m.Resources {
		if r.Mode == "resource" {
			add(&addrBlock{Span: r.Span, addr: r.Type + "." + r.Name, kind: r.Type, body: r.Body})
		}
	}
	for _, c := range m.ModuleCalls {
		add(&addrBlock{Span: c.Span, addr: "module." + c.Name, kind: "module", body: c.Body})
	}
	return out
}

// maxMoveHops bounds a chain of moved blocks the detector follows.
const maxMoveHops = 16

var trailingKey = regexp.MustCompile(`\[([0-9]+|"(?:[^"\\]|\\.)*")\]$`)

// addressMoves are the static moved blocks of the head of one area.
type addressMoves struct {
	from map[string]*Moved
	head map[string]*addrBlock
}

// follow returns where a moved chain from addr ends, and the first moved
// block of the chain, or "" and nil when no moved block names addr.
func (m addressMoves) follow(addr string) (string, *Moved) {
	first := m.from[addr]
	if first == nil {
		return "", nil
	}
	to := first.ToAddr
	for range maxMoveHops {
		next := m.from[to]
		if next == nil || m.head[to] != nil {
			break
		}
		to = next.ToAddr
	}
	return to, first
}

// split returns the head block a target names and the key, or nil when the
// target is in no block of this area. other reports a target in another
// area: an address inside a module call, such as module.child.<type>.<name>.
func (m addressMoves) split(target string) (b *addrBlock, key string, other bool) {
	if b := m.head[target]; b != nil {
		return b, "", false
	}
	if loc := trailingKey.FindStringIndex(target); loc != nil {
		if b := m.head[target[:loc[0]]]; b != nil {
			return b, target[loc[0]:], false
		}
	}
	return nil, "", strings.Count(target, "module.") > 0 && !moduleInstance(target)
}

// moduleInstance reports a target that is a module call of this area, or
// one of its instances, rather than an address inside one.
func moduleInstance(target string) bool {
	rest := strings.TrimPrefix(target, "module.")
	if loc := trailingKey.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return !strings.ContainsAny(rest, ".[")
}

// keyFits reports a key that a block with the given keys has, directly or
// by Terraform's implicit move between no key and [0].
func keyFits(k string, keys []string) bool { return slices.Contains(keys, k) || implicitKey(k, keys) }

// placed reports whether instance k of a base block has a place at the head:
// a static moved block for that instance or for the whole address, whose
// target block at the head has that key, or the same address at the head
// with that key. A move for one instance into another area, a target the
// head lacks and keys that are not static leave it without one.
func (m addressMoves) placed(b *addrBlock, k string, bKeysOK bool) (ok bool, mv *Moved) {
	if to, mv := m.follow(b.addr + k); k != "" && mv != nil {
		t, key, _ := m.split(to)
		if t == nil {
			return false, mv
		}
		keys, kok := instanceKeys(t.body)
		return kok && keyFits(key, keys), mv
	}
	if to, mv := m.follow(b.addr); mv != nil {
		t, key, other := m.split(to)
		switch {
		case other:
			return true, mv // a whole address moved into another area
		case t == nil:
			return false, mv
		case key != "":
			// To one instance: only an address with no key fits there.
			keys, kok := instanceKeys(t.body)
			return bKeysOK && k == "" && kok && keyFits(key, keys), mv
		case !bKeysOK:
			return modeText(repetition(b.body)) == modeText(repetition(t.body)), mv
		}
		keys, kok := instanceKeys(t.body)
		return kok && keyFits(k, keys), mv
	}
	h := m.head[b.addr]
	if h == nil || !bKeysOK {
		return false, nil
	}
	keys, kok := instanceKeys(h.body)
	return kok && keyFits(k, keys), nil
}

// checkResourceAddressChanged: a managed resource or module call address at
// the base, with its instance keys, that has no place at the head; see
// placed. Moved blocks are read from the head directly, whether the change
// touched them or not. A removed block, a moved block with an expression
// key and keys that are not static leave the address to the confirmer: they
// are candidates, never clean. A resource of a submodule moved to the root
// stays a candidate in the submodule: callers of the submodule itself still
// lose it.
func checkResourceAddressChanged(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.resource-address-changed"
	base, head := addrBlocks(a.Base), addrBlocks(a.Head)
	moves := addressMoves{from: map[string]*Moved{}, head: head}
	movedTo := map[string]bool{}
	for i := range a.Head.Moved {
		m := &a.Head.Moved[i]
		if m.FromAddr == "" || m.ToAddr == "" {
			continue // an expression key maps nothing the detector can name
		}
		if moves.from[m.FromAddr] == nil {
			moves.from[m.FromAddr] = m
		}
		if t, _, _ := moves.split(m.ToAddr); t != nil {
			movedTo[t.addr] = true
		}
	}
	removedBlock := map[string]*Removed{}
	for i := range a.Head.Removed {
		if r := &a.Head.Removed[i]; r.FromAddr != "" && removedBlock[r.FromAddr] == nil {
			removedBlock[r.FromAddr] = r
		}
	}
	// A head address the base lacks and no moved block names: the other
	// half of a rename, paired in order with the removals of its kind.
	var added []*addrBlock
	for addr, h := range head {
		if base[addr] == nil && !movedTo[addr] {
			added = append(added, h)
		}
	}
	sort.Slice(added, func(i, j int) bool { return lessSpan(added[i].Span, added[j].Span) })

	var res Result
	addrs := slices.Collect(maps.Keys(base))
	sort.Slice(addrs, func(i, j int) bool { return lessSpan(base[addrs[i]].Span, base[addrs[j]].Span) })
	for _, addr := range addrs {
		b, h := base[addr], head[addr]
		if h != nil && modeText(repetition(b.body)) == modeText(repetition(h.body)) {
			continue // the same address and the same repetition give the same instances
		}
		bKeys, bOK := instanceKeys(b.body)
		if !bOK {
			bKeys = []string{""} // one pass, for the whole address
		}
		var lostBy *Moved
		lost := false
		for _, k := range bKeys {
			if ok, mv := moves.placed(b, k, bOK); !ok {
				lost = true
				if lostBy == nil {
					lostBy = mv
				}
			}
		}
		if lost {
			res.Candidates = append(res.Candidates, addressCandidate(a, id, b, h, removedBlock[addr], lostBy, &added))
		}
	}
	return res
}

// addressCandidate is the candidate for one base address. Its line is a
// head line the change made: the head repetition argument, the removed
// block, the moved block that leaves an instance without a place, or the
// block that took its place in a rename. With none it has no line and names
// the base file.
func addressCandidate(a *Area, id string, b, h *addrBlock, rm *Removed, mv *Moved, added *[]*addrBlock) Candidate {
	ev := newResEvidence(a)
	bMode, bArg := repetition(b.body)
	ev.line("merge_base", b.File, b.Line)
	if bArg != nil {
		ev.line("merge_base", b.File, bArg.Range.Start.Line)
	}
	switch {
	case mv != nil:
		ev.line("head", mv.File, mv.Line)
		ev.values(new(b.addr), new(mv.ToAddr))
		return ev.candidate(id, mv.File, mv.Line, b.addr)
	case h != nil:
		hMode, hArg := repetition(h.body)
		ev.values(new(modeText(bMode, bArg)), new(modeText(hMode, hArg)))
		// A repetition argument on one head line that differs from the base
		// is on a changed line; any other edit has no such line.
		if hArg != nil && (bArg == nil || hArg.Value.Text != bArg.Value.Text) && hArg.Range.Start.Line == hArg.Range.End.Line {
			line := hArg.Range.Start.Line
			ev.line("head", h.File, line)
			return ev.candidate(id, h.File, line, b.addr)
		}
		return ev.candidate(id, b.File, 0, b.addr)
	case rm != nil:
		ev.line("head", rm.File, rm.Line)
		ev.values(new(b.addr), nil)
		return ev.candidate(id, rm.File, rm.Line, b.addr)
	}
	if i := slices.IndexFunc(*added, func(n *addrBlock) bool { return n.kind == b.kind }); i >= 0 {
		n := (*added)[i]
		*added = slices.Delete(*added, i, i+1)
		ev.line("head", n.File, n.Line)
		ev.values(new(b.addr), new(n.addr))
		return ev.candidate(id, n.File, n.Line, n.addr)
	}
	ev.values(new(b.addr), nil)
	return ev.candidate(id, b.File, 0, b.addr)
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
