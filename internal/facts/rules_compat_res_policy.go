package facts

// The generated policy check of the compat_res family, whose rules are in
// rules_compat_res.go.

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

const policyDocType = "aws_iam_policy_document"

// policyAttr reports an argument that may hold a policy document the module
// produces: any name with "polic" in it, such as policy, assume_role_policy,
// managed_policies or policy_document. No list of names is exhaustive, so
// the match is wide: an edit to such an argument is a candidate.
func policyAttr(name string) bool { return strings.Contains(name, "polic") }

// widensIn reports an aws_iam_policy_document argument whose literal list
// only widens an allow statement when it gains entries, inside a block of
// type blockType of the base and head bodies b and h: actions and resources
// of a statement, identifiers of principals (never not_principals), and
// values of a condition whose test is the same literal at both, with no Not
// in it. A test such as StringNotEquals narrows as its values grow.
func widensIn(name, blockType string, b, h Body) bool {
	switch {
	case blockType == "statement":
		return name == "actions" || name == "resources"
	case blockType == "principals":
		return name == "identifiers"
	case blockType == "condition" && name == "values":
		bt, ht := bodyAttr(b, "test"), bodyAttr(h, "test")
		if bt == nil || ht == nil || bt.Value.Text != ht.Value.Text {
			return false
		}
		e, ok := parseExprAt(ht.Value)
		if !ok {
			return false
		}
		v, ok := literalValue(e)
		return ok && !v.IsNull() && v.Type() == cty.String && !strings.Contains(v.AsString(), "Not")
	}
	return false
}

type policyChange struct {
	line       int // 0: the line of the enclosing head block
	path       string
	base, head *string
}

// checkPolicyNarrowed: a policy document the module produces that may
// permit less at the head. An aws_iam_policy_document is compared argument
// by argument: an allow statement that only gains literal entries where
// widensIn allows it, or a new allow statement, is a widening; any other
// edit is a candidate. Any other policy argument,
// such as a jsonencode() document, is a candidate when its text changes. A
// local the policy text reads, directly or through other locals, is a
// candidate when the change edits it. A removed document data block is a
// candidate with no head line. Variable defaults that feed a document are
// compat.default-changed's.
func checkPolicyNarrowed(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.generated-policy-narrowed"
	var res Result
	baseBlocks := map[string]*Resource{}
	for i := range a.Base.Resources {
		r := &a.Base.Resources[i]
		if k := coverageBlockAddr(*r); baseBlocks[k] == nil {
			baseBlocks[k] = r
		}
	}
	headSeen := map[string]bool{}
	var policyText []string
	for i := range a.Head.Resources {
		h := &a.Head.Resources[i]
		k := coverageBlockAddr(*h)
		if h.Mode == "data" && h.Type == policyDocType {
			policyText = append(policyText, bodyText(h.Body)...)
		} else {
			policyText = append(policyText, policyTexts(h.Body)...)
		}
		if headSeen[k] {
			continue
		}
		headSeen[k] = true
		b := baseBlocks[k]
		if b == nil {
			continue
		}
		var changes []policyChange
		if h.Mode == "data" && h.Type == policyDocType {
			docDiff(b.Body, h.Body, "", "", h.Line, true, &changes)
		} else {
			policyArgDiff(b.Body, h.Body, "", h.Line, &changes)
		}
		for _, c := range changes {
			ev := newResEvidence(a)
			line := c.line
			if line == 0 {
				line = h.Line
			}
			ev.line("head", h.File, line)
			ev.values(c.base, c.head)
			res.Candidates = append(res.Candidates, ev.candidate(id, h.File, line, k+c.path))
		}
	}
	for _, k := range resSortedKeys(baseBlocks) {
		b := baseBlocks[k]
		if b.Mode == "data" && b.Type == policyDocType && !headSeen[k] {
			ev := newResEvidence(a)
			ev.line("merge_base", b.File, b.Line)
			ev.values(new(k), nil)
			res.Candidates = append(res.Candidates, ev.candidate(id, b.File, 0, k))
		}
	}
	res.Candidates = append(res.Candidates, policyLocals(a, id, policyText)...)
	return res
}

var localRef = regexp.MustCompile(`\blocal\.([A-Za-z_][A-Za-z0-9_-]*)`)

// policyLocals are the candidates for the locals the policy text reads,
// directly or through other locals, that the change edits.
func policyLocals(a *Area, id string, texts []string) []Candidate {
	head := map[string]*Local{}
	for i := range a.Head.Locals {
		head[a.Head.Locals[i].Name] = &a.Head.Locals[i]
	}
	base := map[string]*Local{}
	for i := range a.Base.Locals {
		base[a.Base.Locals[i].Name] = &a.Base.Locals[i]
	}
	read := map[string]bool{}
	queue := texts
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for _, m := range localRef.FindAllStringSubmatch(t, -1) {
			if n := m[1]; !read[n] {
				read[n] = true
				if l := head[n]; l != nil {
					queue = append(queue, l.Value.Text)
				}
			}
		}
	}
	var out []Candidate
	for _, n := range resSortedKeys(read) {
		h, b := head[n], base[n]
		if h == nil || b == nil || h.Value.Text == b.Value.Text {
			continue // a new local is read by policy text the change also edits
		}
		ev := newResEvidence(a)
		ev.line("merge_base", b.File, b.Line)
		ev.line("head", h.File, h.Line)
		ev.values(new(b.Value.Text), new(h.Value.Text))
		out = append(out, ev.candidate(id, h.File, h.Line, "local."+n))
	}
	return out
}

// bodyText is the text of every argument of a body, nested blocks included.
func bodyText(b Body) []string {
	var out []string
	for _, a := range b.Attributes {
		out = append(out, a.Value.Text)
	}
	for _, nb := range b.Blocks {
		if nb.ForEach != nil {
			out = append(out, nb.ForEach.Text)
		}
		out = append(out, bodyText(nb.Body)...)
	}
	return out
}

// policyTexts is the text of every policy argument of a body.
func policyTexts(b Body) []string {
	var out []string
	for _, a := range b.Attributes {
		if policyAttr(a.Name) {
			out = append(out, a.Value.Text)
		}
	}
	for _, nb := range b.Blocks {
		out = append(out, policyTexts(nb.Body)...)
	}
	return out
}

// staticBlocks groups the nested blocks of a body by type, static blocks
// with their index and dynamic blocks apart.
func staticBlocks(b Body) (static map[string][]*NestedBlock, dynamic map[string][]*NestedBlock) {
	static, dynamic = map[string][]*NestedBlock{}, map[string][]*NestedBlock{}
	for i := range b.Blocks {
		nb := &b.Blocks[i]
		if nb.Dynamic {
			dynamic[nb.Type] = append(dynamic[nb.Type], nb)
		} else {
			static[nb.Type] = append(static[nb.Type], nb)
		}
	}
	return static, dynamic
}

// policyArgDiff adds a change for each policy argument of a block, at any
// depth, that the change adds, removes or edits.
func policyArgDiff(base, head Body, path string, line int, out *[]policyChange) {
	for _, h := range head.Attributes {
		if !policyAttr(h.Name) {
			continue
		}
		if b := bodyAttr(base, h.Name); b == nil || b.Value.Text != h.Value.Text {
			c := policyChange{line: h.Range.Start.Line, path: path + "." + h.Name, head: new(h.Value.Text)}
			if b != nil {
				c.base = new(b.Value.Text)
			}
			*out = append(*out, c)
		}
	}
	for _, b := range base.Attributes {
		if policyAttr(b.Name) && bodyAttr(head, b.Name) == nil {
			*out = append(*out, policyChange{line: line, path: path + "." + b.Name, base: new(b.Value.Text)})
		}
	}
	bs, bd := staticBlocks(base)
	hs, hd := staticBlocks(head)
	for _, t := range resSortedKeys(hs) {
		for i, h := range hs[t] {
			sub := path + "." + t + "[" + strconv.Itoa(i) + "]"
			var bb Body
			if i < len(bs[t]) {
				bb = bs[t][i].Body
			}
			policyArgDiff(bb, h.Body, sub, h.Range.Start.Line, out)
		}
	}
	for _, t := range resSortedKeys(bs) {
		for i := len(hs[t]); i < len(bs[t]); i++ {
			for _, p := range policyTexts(bs[t][i].Body) {
				*out = append(*out, policyChange{line: line, path: path + "." + t + "[" + strconv.Itoa(i) + "]", base: new(p)})
			}
		}
	}
	for _, t := range resSortedKeys(hd) {
		policyDynamicDiff(bd[t], hd[t], path+"."+t, line, out, func(b Body) bool { return len(policyTexts(b)) > 0 })
	}
	for _, t := range resSortedKeys(bd) {
		if hd[t] == nil {
			policyDynamicDiff(bd[t], nil, path+"."+t, line, out, func(b Body) bool { return len(policyTexts(b)) > 0 })
		}
	}
}

// policyDynamicDiff adds one change when the dynamic blocks of one type
// differ and relevant says one of them holds policy text.
func policyDynamicDiff(base, head []*NestedBlock, path string, line int, out *[]policyChange, relevant func(Body) bool) {
	if dynText(base) == dynText(head) {
		return
	}
	for _, nb := range slices.Concat(base, head) {
		if relevant(nb.Body) {
			l := line
			if len(head) > 0 {
				l = head[0].Range.Start.Line
			}
			*out = append(*out, policyChange{line: l, path: path, base: new(dynText(base)), head: new(dynText(head))})
			return
		}
	}
}

func dynText(bs []*NestedBlock) string {
	var parts []string
	for _, nb := range bs {
		if nb.ForEach != nil {
			parts = append(parts, nb.ForEach.Text)
		}
		parts = append(parts, nb.Iterator)
		parts = append(parts, bodyText(nb.Body)...)
		for _, a := range nb.Body.Attributes {
			parts = append(parts, a.Name)
		}
	}
	return strings.Join(parts, "\x00")
}

// allowEffect reports a statement whose effect is unset or the literal Allow.
func allowEffect(b Body) bool {
	e := bodyAttr(b, "effect")
	return e == nil || e.Value.Text == `"Allow"`
}

// stringSet is the set of a literal list of strings, or false.
func stringSet(x Expr) (map[string]bool, bool) {
	e, ok := parseExprAt(x)
	if !ok {
		return nil, false
	}
	v, ok := literalValue(e)
	if !ok || v.IsNull() {
		return nil, false
	}
	if t := v.Type(); !t.IsTupleType() && !t.IsListType() && !t.IsSetType() {
		return nil, false
	}
	out := map[string]bool{}
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.IsNull() || ev.Type() != cty.String {
			return nil, false
		}
		out[ev.AsString()] = true
	}
	return out, true
}

// widens reports a literal list at the head that holds every entry of the
// base list.
func widens(b, h Expr) bool {
	bs, ok1 := stringSet(b)
	hs, ok2 := stringSet(h)
	if !ok1 || !ok2 {
		return false
	}
	for s := range bs {
		if !hs[s] {
			return false
		}
	}
	return true
}

// docDiff compares two bodies of an aws_iam_policy_document, inside a
// nested block of type blockType, or "" at the document level. allow is set
// inside an allow statement, or at the document level, where a new allow
// statement only widens the document.
func docDiff(base, head Body, blockType, path string, line int, allow bool, out *[]policyChange) {
	for _, h := range head.Attributes {
		if h.Name == "sid" {
			continue
		}
		b := bodyAttr(base, h.Name)
		switch {
		case b != nil && b.Value.Text == h.Value.Text:
		case b != nil && allow && widensIn(h.Name, blockType, base, head) && widens(b.Value, h.Value):
		default:
			c := policyChange{line: h.Range.Start.Line, path: path + "." + h.Name, head: new(h.Value.Text)}
			if b != nil {
				c.base = new(b.Value.Text)
			}
			*out = append(*out, c)
		}
	}
	for _, b := range base.Attributes {
		if b.Name != "sid" && bodyAttr(head, b.Name) == nil {
			*out = append(*out, policyChange{line: line, path: path + "." + b.Name, base: new(b.Value.Text)})
		}
	}
	bs, bd := staticBlocks(base)
	hs, hd := staticBlocks(head)
	for _, t := range resSortedKeys(hs) {
		for i, h := range hs[t] {
			sub := path + "." + t + "[" + strconv.Itoa(i) + "]"
			if i >= len(bs[t]) {
				if path != "" || t != "statement" || !allowEffect(h.Body) {
					*out = append(*out, policyChange{line: h.Range.Start.Line, path: sub})
				}
				continue
			}
			inner := allow
			if t == "statement" {
				inner = allow && allowEffect(bs[t][i].Body) && allowEffect(h.Body)
			}
			docDiff(bs[t][i].Body, h.Body, t, sub, h.Range.Start.Line, inner, out)
		}
	}
	for _, t := range resSortedKeys(bs) {
		for i := len(hs[t]); i < len(bs[t]); i++ {
			*out = append(*out, policyChange{line: line, path: path + "." + t + "[" + strconv.Itoa(i) + "]"})
		}
	}
	all := func(Body) bool { return true }
	for _, t := range resSortedKeys(hd) {
		policyDynamicDiff(bd[t], hd[t], path+"."+t, line, out, all)
	}
	for _, t := range resSortedKeys(bd) {
		if hd[t] == nil {
			policyDynamicDiff(bd[t], nil, path+"."+t, line, out, all)
		}
	}
}
