package facts

// The statement comparison of compat.generated-policy-narrowed: the
// statements of an aws_iam_policy_document are compared by content, not by
// position, so an inserted statement shifts nothing and a change that only
// adds permissions raises nothing.

import (
	"slices"
	"strconv"
	"strings"
)

// Statement kinds by effect: unset or "Allow", "Deny", or any other text.
const (
	effectAllow = "allow"
	effectDeny  = "deny"
	effectOther = "other"
)

// mergeArgs are the document arguments that merge statements by sid, so
// that a sid decides which statement wins.
var mergeArgs = []string{"source_policy_documents", "override_policy_documents", "source_json", "override_json"}

// stmt is one statement block of a document, static or dynamic, with its
// path in candidate subjects: statement[i] among the static statements and
// dynamic.statement[j] among the dynamic ones.
type stmt struct {
	nb   *NestedBlock
	path string
	kind string
}

func (s stmt) line() int { return s.nb.Range.Start.Line }

// docStatements lists the statements of a document body in source order.
func docStatements(b Body) []stmt {
	var out []stmt
	ns, nd := 0, 0
	for i := range b.Blocks {
		nb := &b.Blocks[i]
		if nb.Type != "statement" {
			continue
		}
		s := stmt{nb: nb, kind: effectKind(nb.Body)}
		if nb.Dynamic {
			s.path = ".dynamic.statement[" + strconv.Itoa(nd) + "]"
			nd++
		} else {
			s.path = ".statement[" + strconv.Itoa(ns) + "]"
			ns++
		}
		out = append(out, s)
	}
	return out
}

// effectKind is the kind of a statement by its effect argument.
func effectKind(b Body) string {
	e := bodyAttr(b, "effect")
	if e == nil {
		return effectAllow
	}
	switch v, ok := literalArg(e.Value); {
	case ok && v == "Allow":
		return effectAllow
	case ok && v == "Deny":
		return effectDeny
	}
	return effectOther
}

// literalArg is the value of a literal string expression.
func literalArg(x Expr) (string, bool) {
	e, ok := parseExprAt(x)
	if !ok {
		return "", false
	}
	return literalString(e)
}

// withoutStatements is a body with its statement blocks left out.
func withoutStatements(b Body) Body {
	out := Body{Attributes: b.Attributes}
	for _, nb := range b.Blocks {
		if nb.Type != "statement" {
			out.Blocks = append(out.Blocks, nb)
		}
	}
	return out
}

// maxPolicyPairs caps the base and head statement pairs one document
// compares by content. Past it, statements match by their exact text, which
// can only raise more candidates.
const maxPolicyPairs = 10000

// docMemo holds what a document comparison reads more than once: the
// normalized text and the literal value of each expression text, and the
// text of each block, so each is parsed once however many pairs read it.
type docMemo struct {
	norms  map[string]string
	sets   map[string]litSet
	strs   map[string]litStr
	blocks map[*NestedBlock]string
}

type litSet struct {
	set map[string]bool
	ok  bool
}

type litStr struct {
	s  string
	ok bool
}

func newDocMemo() *docMemo {
	return &docMemo{norms: map[string]string{}, sets: map[string]litSet{}, strs: map[string]litStr{}, blocks: map[*NestedBlock]string{}}
}

func (m *docMemo) norm(s string) string {
	n, ok := m.norms[s]
	if !ok {
		n = normExpr(s)
		m.norms[s] = n
	}
	return n
}

func (m *docMemo) normExpr(e *Expr) string {
	if e == nil {
		return ""
	}
	return m.norm(e.Text)
}

// set is stringSet, by expression text.
func (m *docMemo) set(x Expr) (map[string]bool, bool) {
	v, ok := m.sets[x.Text]
	if !ok {
		v.set, v.ok = stringSet(x)
		m.sets[x.Text] = v
	}
	return v.set, v.ok
}

// str is the value of a literal string expression, by expression text.
func (m *docMemo) str(x Expr) (string, bool) {
	v, ok := m.strs[x.Text]
	if !ok {
		v.s, v.ok = literalArg(x)
		m.strs[x.Text] = v
	}
	return v.s, v.ok
}

// widens reports a literal list h that holds every entry of the literal
// list b.
func (m *docMemo) widens(b, h Expr) bool {
	bs, ok1 := m.set(b)
	hs, ok2 := m.set(h)
	return ok1 && ok2 && subsetOf(bs, hs)
}

// block is the normalized text of a block: its type, labels, dynamic
// arguments, arguments by name and nested blocks in order.
func (m *docMemo) block(nb *NestedBlock) string {
	if t, ok := m.blocks[nb]; ok {
		return t
	}
	parts := []string{nb.Type, strings.Join(nb.Labels, " "), strconv.FormatBool(nb.Dynamic), m.normExpr(nb.ForEach), nb.Iterator}
	attrs := slices.Clone(nb.Body.Attributes)
	slices.SortFunc(attrs, func(a, b Attribute) int { return strings.Compare(a.Name, b.Name) })
	for _, a := range attrs {
		parts = append(parts, a.Name+" = "+m.norm(a.Value.Text))
	}
	for i := range nb.Body.Blocks {
		parts = append(parts, "{"+m.block(&nb.Body.Blocks[i])+"}")
	}
	t := strings.Join(parts, "\n")
	m.blocks[nb] = t
	return t
}

func (m *docMemo) bodyText(b Body) string { return m.block(&NestedBlock{Body: b}) }

// docCompare compares two bodies of an aws_iam_policy_document. Document
// arguments and blocks other than statements are compared as docDiff does.
// A base allow, or other, statement that no head statement covers is a
// candidate, reported against a partner head statement where one exists; a
// head deny, or other, statement that no base statement of its kind covers
// is a candidate. A new allow statement and a removed deny only widen.
func docCompare(base, head Body, line int, out *[]policyChange) {
	docDiff(withoutStatements(base), withoutStatements(head), "", "", line, true, out)
	m := newDocMemo()
	merge := slices.ContainsFunc(mergeArgs, func(n string) bool { return bodyAttr(base, n) != nil || bodyAttr(head, n) != nil })
	bs, hs := docStatements(base), docStatements(head)
	c := docCmp{m: m, merge: merge, exact: len(bs)*len(hs) > maxPolicyPairs}
	reported := map[string]bool{} // head statements a base one was reported against
	for _, b := range bs {
		if b.kind == effectDeny || c.coveredBy(b, hs) {
			continue
		}
		p := c.partner(b, hs)
		if p == nil {
			*out = append(*out, policyChange{line: line, path: b.path, base: new(stmtSource(b.nb))})
			continue
		}
		reported[p.path] = true
		n := len(*out)
		if p.nb.Dynamic && (m.normExpr(p.nb.ForEach) != m.normExpr(b.nb.ForEach) || p.nb.Iterator != b.nb.Iterator) {
			*out = append(*out, policyChange{line: p.line(), path: p.path + ".for_each", base: new(m.normExpr(b.nb.ForEach)), head: new(m.normExpr(p.nb.ForEach))})
		}
		docDiff(b.nb.Body, p.nb.Body, "statement", p.path, p.line(), b.kind == effectAllow, out)
		switch {
		case len(*out) == n:
			*out = append(*out, policyChange{line: p.line(), path: p.path, base: new(stmtSource(b.nb)), head: new(stmtSource(p.nb))})
		case p.nb.Dynamic:
			// A dynamic statement is one candidate, at its first change: its
			// content is mostly expressions, edited together.
			first := (*out)[n].line
			*out = append((*out)[:n], policyChange{line: first, path: p.path, base: new(stmtSource(b.nb)), head: new(stmtSource(p.nb))})
		}
	}
	baseSids := map[string]bool{}
	for _, b := range bs {
		if a := bodyAttr(b.nb.Body, "sid"); a != nil {
			baseSids[m.norm(a.Value.Text)] = true
		}
	}
	for _, h := range hs {
		newSid := false
		if a := bodyAttr(h.nb.Body, "sid"); merge && a != nil {
			// The sid may replace a statement a merged document brings.
			newSid = !baseSids[m.norm(a.Value.Text)]
		}
		if (h.kind == effectAllow || c.contained(h, bs)) && !newSid || reported[h.path] {
			continue
		}
		n := len(*out)
		if p := c.partner(h, bs); p != nil && !newSid {
			docDiff(p.nb.Body, h.nb.Body, "statement", h.path, h.line(), false, out)
		}
		if len(*out) == n {
			*out = append(*out, policyChange{line: h.line(), path: h.path, head: new(stmtSource(h.nb))})
		}
	}
}

// docCmp compares the statements of one document. merge is set when the
// document merges statements by sid; exact when it has more statement pairs
// than maxPolicyPairs, so statements match only by their exact text.
type docCmp struct {
	m     *docMemo
	merge bool
	exact bool
}

// contained reports a head statement that some base statement of its kind
// covers: a deny the base already denied at least.
func (c docCmp) contained(h stmt, bs []stmt) bool {
	if c.exact {
		return slices.ContainsFunc(bs, func(b stmt) bool { return c.same(h, b) })
	}
	return slices.ContainsFunc(bs, func(b stmt) bool { return c.covers(h, b, "") })
}

// same reports two statements of one kind and form with equal text.
func (c docCmp) same(a, b stmt) bool {
	return a.kind == b.kind && a.nb.Dynamic == b.nb.Dynamic && c.m.block(a.nb) == c.m.block(b.nb)
}

// coveredBy reports a base statement that the head statements grant at
// least: one head statement covers it, or for one dimension, actions or
// resources, the head statements that cover it on everything else hold a
// literal superset of its literal entries between them. A head statement
// without that dimension adds nothing to it. Union is for allow statements
// only; it is one pass over the head statements per dimension.
func (c docCmp) coveredBy(b stmt, hs []stmt) bool {
	if c.exact {
		return slices.ContainsFunc(hs, func(h stmt) bool { return c.same(b, h) })
	}
	if slices.ContainsFunc(hs, func(h stmt) bool { return c.covers(b, h, "") }) {
		return true
	}
	if b.kind != effectAllow {
		return false
	}
	for _, dim := range []string{"actions", "resources"} {
		a := bodyAttr(b.nb.Body, dim)
		if a == nil {
			continue
		}
		want, ok := c.m.set(a.Value)
		if !ok || len(want) == 0 {
			continue
		}
		have := map[string]bool{}
		for _, h := range hs {
			ha := bodyAttr(h.nb.Body, dim)
			if ha == nil || !c.covers(b, h, dim) {
				continue
			}
			if set, ok := c.m.set(ha.Value); ok {
				for s := range set {
					have[s] = true
				}
			}
		}
		if len(have) > 0 && subsetOf(want, have) {
			return true
		}
	}
	return false
}

func subsetOf(a, b map[string]bool) bool {
	for s := range a {
		if !b[s] {
			return false
		}
	}
	return true
}

// covers reports that statement outer grants at least what inner grants,
// for statements of one kind: for allow, outer allows at least what inner
// allows, and for deny, outer denies at least what inner denies. Every
// argument and block type is on both sides or on neither, apart from
// conditions and the sid, which counts only when merge is set. actions and
// resources may grow and not_actions and not_resources shrink as literal
// lists; principals may gain identifiers of a type; outer's conditions are
// each one of inner's, its values grown under a literal test with no Not.
// Nested dynamic blocks pair in order, see dynamicCover. Anything else
// compares by its normalized text, as does a whole statement whose effect
// is neither Allow nor Deny, so what the comparison does not model can
// never hide a narrowing. skip names one dimension left out, for union.
func (c docCmp) covers(inner, outer stmt, skip string) bool {
	m := c.m
	if inner.kind != outer.kind || inner.nb.Dynamic != outer.nb.Dynamic {
		return false
	}
	if inner.nb.Dynamic && (m.normExpr(inner.nb.ForEach) != m.normExpr(outer.nb.ForEach) || inner.nb.Iterator != outer.nb.Iterator) {
		return false
	}
	ib, ob := inner.nb.Body, outer.nb.Body
	if inner.kind == effectOther {
		return skip == "" && m.block(inner.nb) == m.block(outer.nb)
	}
	if !c.dynamicCover(ib, ob) {
		return false
	}
	names := map[string]bool{}
	for _, a := range slices.Concat(ib.Attributes, ob.Attributes) {
		names[a.Name] = true
	}
	for n := range names {
		if n == "effect" || n == skip || (n == "sid" && !c.merge) {
			continue
		}
		i, o := bodyAttr(ib, n), bodyAttr(ob, n)
		if i == nil || o == nil || !c.argCovers(n, i.Value, o.Value) {
			return false
		}
	}
	is, _ := staticBlocks(ib)
	os, _ := staticBlocks(ob)
	types := map[string]bool{}
	for t := range is {
		types[t] = true
	}
	for t := range os {
		types[t] = true
	}
	for t := range types {
		var ok bool
		switch t {
		case "principals":
			ok = c.principalsCover(is[t], os[t])
		case "condition":
			ok = c.conditionsCover(is[t], os[t])
		default:
			ok = c.sameBlocks(is[t], os[t])
		}
		if !ok {
			return false
		}
	}
	return true
}

// argCovers compares one statement argument present on both sides.
func (c docCmp) argCovers(name string, i, o Expr) bool {
	if c.m.norm(i.Text) == c.m.norm(o.Text) {
		return true
	}
	switch name {
	case "actions", "resources":
		return c.m.widens(i, o)
	case "not_actions", "not_resources":
		s, ok := c.m.set(o)
		return ok && len(s) > 0 && c.m.widens(o, i)
	}
	return false
}

// principalsCover: both sides have principals or neither has, and each
// inner principals block has an outer one of the same type whose
// identifiers are equal or a literal superset.
func (c docCmp) principalsCover(inner, outer []*NestedBlock) bool {
	m := c.m
	if (len(inner) == 0) != (len(outer) == 0) {
		return false
	}
	for _, i := range inner {
		if !slices.ContainsFunc(outer, func(o *NestedBlock) bool {
			if len(i.Body.Blocks) > 0 || len(o.Body.Blocks) > 0 || len(i.Body.Attributes) != 2 || len(o.Body.Attributes) != 2 {
				return m.block(i) == m.block(o)
			}
			it, ot := bodyAttr(i.Body, "type"), bodyAttr(o.Body, "type")
			ii, oi := bodyAttr(i.Body, "identifiers"), bodyAttr(o.Body, "identifiers")
			return it != nil && ot != nil && ii != nil && oi != nil && m.norm(it.Value.Text) == m.norm(ot.Value.Text) &&
				(m.norm(ii.Value.Text) == m.norm(oi.Value.Text) || m.widens(ii.Value, oi.Value))
		}) {
			return false
		}
	}
	return true
}

// conditionsCover: each outer condition is one of inner's, with the same
// test and variable and values equal or grown under a literal test with no
// Not. Conditions are ANDed, so fewer outer conditions allow more. When a
// test and variable pair repeats on either side, the conditions must match
// as a multiset of text.
func (c docCmp) conditionsCover(inner, outer []*NestedBlock) bool {
	for _, side := range [][]*NestedBlock{inner, outer} {
		seen := map[string]bool{}
		for _, b := range side {
			k, ok := c.conditionKey(b)
			if !ok || seen[k] {
				return c.sameBlocks(inner, outer)
			}
			seen[k] = true
		}
	}
	return c.conditionsMatch(inner, outer)
}

// conditionKey is the test and variable of a condition block that holds
// exactly test, variable and values, or false for any other shape.
func (c docCmp) conditionKey(b *NestedBlock) (string, bool) {
	t, v := bodyAttr(b.Body, "test"), bodyAttr(b.Body, "variable")
	if t == nil || v == nil || bodyAttr(b.Body, "values") == nil || len(b.Body.Attributes) != 3 || len(b.Body.Blocks) > 0 {
		return "", false
	}
	return c.m.norm(t.Value.Text) + "\x00" + c.m.norm(v.Value.Text), true
}

// conditionsMatch: each outer condition has an inner one with the same key
// whose values it equals, or holds as a literal superset under a literal
// test with no Not.
func (c docCmp) conditionsMatch(inner, outer []*NestedBlock) bool {
	for _, o := range outer {
		ok := slices.ContainsFunc(inner, func(i *NestedBlock) bool {
			ik, _ := c.conditionKey(i)
			ok, _ := c.conditionKey(o)
			if ik != ok {
				return false
			}
			iv, ov := bodyAttr(i.Body, "values"), bodyAttr(o.Body, "values")
			if c.m.norm(iv.Value.Text) == c.m.norm(ov.Value.Text) {
				return true
			}
			test, lit := c.m.str(bodyAttr(o.Body, "test").Value)
			return lit && !strings.Contains(test, "Not") && c.m.widens(iv.Value, ov.Value)
		})
		if !ok {
			return false
		}
	}
	return true
}

// sameBlocks reports two lists of blocks equal as multisets of text.
func (c docCmp) sameBlocks(a, b []*NestedBlock) bool {
	if len(a) != len(b) {
		return false
	}
	ta, tb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ta[i], tb[i] = c.m.block(a[i]), c.m.block(b[i])
	}
	slices.Sort(ta)
	slices.Sort(tb)
	return slices.Equal(ta, tb)
}

// dynamicCover compares the nested dynamic blocks of two statements: the
// same number, paired in order, each pair with the same type, for_each and
// iterator. A dynamic condition may grow its values as a static one may.
// When its test and variable are also those of a static condition on
// either side, the provider merges the two, so the whole statement bodies
// must be equal by text. Any other pair compares by text.
func (c docCmp) dynamicCover(ib, ob Body) bool {
	m := c.m
	id, od := dynamicBlocks(ib), dynamicBlocks(ob)
	if len(id) != len(od) {
		return false
	}
	if len(id) == 0 {
		return true
	}
	is, _ := staticBlocks(ib)
	os, _ := staticBlocks(ob)
	static := map[string]bool{}
	for _, b := range slices.Concat(is["condition"], os["condition"]) {
		if k, ok := c.conditionKey(b); ok {
			static[k] = true
		}
	}
	for n, i := range id {
		o := od[n]
		if i.Type != o.Type || m.normExpr(i.ForEach) != m.normExpr(o.ForEach) || i.Iterator != o.Iterator {
			return false
		}
		ik, iok := c.conditionKey(i)
		if iok && static[ik] {
			return m.bodyText(ib) == m.bodyText(ob)
		}
		if i.Type != "condition" || !iok {
			if m.block(i) != m.block(o) {
				return false
			}
			continue
		}
		if !c.conditionsMatch([]*NestedBlock{i}, []*NestedBlock{o}) {
			return false
		}
	}
	return true
}

func dynamicBlocks(b Body) []*NestedBlock {
	var out []*NestedBlock
	for i := range b.Blocks {
		if b.Blocks[i].Dynamic {
			out = append(out, &b.Blocks[i])
		}
	}
	return out
}

// stmtSource is a block as evidence: its arguments as written and its
// nested blocks, one per line.
func stmtSource(nb *NestedBlock) string {
	var b strings.Builder
	b.WriteString(nb.Type)
	if nb.Dynamic {
		b.WriteString(" (dynamic, for_each = " + exprString(nb.ForEach) + ")")
	}
	b.WriteString(" {")
	for _, a := range nb.Body.Attributes {
		b.WriteString("\n" + a.Name + " = " + a.Value.Text)
	}
	for i := range nb.Body.Blocks {
		b.WriteString("\n" + stmtSource(&nb.Body.Blocks[i]))
	}
	b.WriteString("\n}")
	return b.String()
}

// partner is the statement of the other side that an uncovered statement
// is reported against, of the same static or dynamic form: the one with the
// same literal sid, else the one sharing the most literal actions, the
// first on a tie, else the one at the same path. Past maxPolicyPairs only
// the sid and the path are read.
func (c docCmp) partner(b stmt, hs []stmt) *stmt {
	same := func(h stmt) bool { return h.nb.Dynamic == b.nb.Dynamic }
	if sid, ok := c.attrString(b.nb.Body, "sid"); ok && sid != "" {
		for i := range hs {
			if s, ok := c.attrString(hs[i].nb.Body, "sid"); ok && s == sid && same(hs[i]) {
				return &hs[i]
			}
		}
	}
	if !c.exact {
		want := c.attrSet(b.nb.Body, "actions")
		var best *stmt
		most := 0
		for i := range hs {
			if !same(hs[i]) {
				continue
			}
			n := 0
			for s := range c.attrSet(hs[i].nb.Body, "actions") {
				if want[s] {
					n++
				}
			}
			if n > most {
				best, most = &hs[i], n
			}
		}
		if best != nil {
			return best
		}
	}
	for i := range hs {
		if hs[i].path == b.path {
			return &hs[i]
		}
	}
	return nil
}

func (c docCmp) attrString(b Body, name string) (string, bool) {
	a := bodyAttr(b, name)
	if a == nil {
		return "", false
	}
	return c.m.str(a.Value)
}

func (c docCmp) attrSet(b Body, name string) map[string]bool {
	a := bodyAttr(b, name)
	if a == nil {
		return nil
	}
	s, _ := c.m.set(a.Value)
	return s
}
