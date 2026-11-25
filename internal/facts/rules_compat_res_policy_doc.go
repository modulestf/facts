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

// docCompare compares two bodies of an aws_iam_policy_document. Document
// arguments and blocks other than statements are compared as docDiff does.
// A base allow, or other, statement that no head statement covers is a
// candidate, reported against a partner head statement where one exists; a
// head deny, or other, statement that no base statement of its kind covers
// is a candidate. A new allow statement and a removed deny only widen.
func docCompare(base, head Body, line int, out *[]policyChange) {
	docDiff(withoutStatements(base), withoutStatements(head), "", "", line, true, out)
	merge := slices.ContainsFunc(mergeArgs, func(n string) bool { return bodyAttr(base, n) != nil || bodyAttr(head, n) != nil })
	bs, hs := docStatements(base), docStatements(head)
	reported := map[string]bool{} // head statements a base one was reported against
	for _, b := range bs {
		if b.kind == effectDeny || coveredBy(b, hs, merge) {
			continue
		}
		p := partner(b, hs)
		if p == nil {
			*out = append(*out, policyChange{line: line, path: b.path, base: new(stmtSource(b.nb))})
			continue
		}
		reported[p.path] = true
		n := len(*out)
		if p.nb.Dynamic && (normText(p.nb.ForEach) != normText(b.nb.ForEach) || p.nb.Iterator != b.nb.Iterator) {
			*out = append(*out, policyChange{line: p.line(), path: p.path + ".for_each", base: new(normText(b.nb.ForEach)), head: new(normText(p.nb.ForEach))})
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
			baseSids[normExpr(a.Value.Text)] = true
		}
	}
	for _, h := range hs {
		newSid := false
		if a := bodyAttr(h.nb.Body, "sid"); merge && a != nil {
			// The sid may replace a statement a merged document brings.
			newSid = !baseSids[normExpr(a.Value.Text)]
		}
		contained := h.kind == effectAllow || slices.ContainsFunc(bs, func(b stmt) bool { return covers(h, b, merge, "") })
		if (contained && !newSid) || reported[h.path] {
			continue
		}
		n := len(*out)
		if p := partner(h, bs); p != nil && !newSid {
			docDiff(p.nb.Body, h.nb.Body, "statement", h.path, h.line(), false, out)
		}
		if len(*out) == n {
			*out = append(*out, policyChange{line: h.line(), path: h.path, head: new(stmtSource(h.nb))})
		}
	}
}

// coveredBy reports a base statement that the head statements grant at
// least: one head statement covers it, or for one dimension, actions or
// resources, the head statements that cover it on everything else hold a
// literal superset of its literal entries between them. Union is for allow
// statements only; it is one pass over the head statements per dimension.
func coveredBy(b stmt, hs []stmt, merge bool) bool {
	if slices.ContainsFunc(hs, func(h stmt) bool { return covers(b, h, merge, "") }) {
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
		want, ok := stringSet(a.Value)
		if !ok || len(want) == 0 {
			continue
		}
		have := map[string]bool{}
		for _, h := range hs {
			if !covers(b, h, merge, dim) {
				continue
			}
			if set, ok := stringSet(bodyAttr(h.nb.Body, dim).Value); ok {
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
// never hide a narrowing. skip names
// one dimension left out, for union.
func covers(inner, outer stmt, merge bool, skip string) bool {
	if inner.kind != outer.kind || inner.nb.Dynamic != outer.nb.Dynamic {
		return false
	}
	if inner.nb.Dynamic && (normText(inner.nb.ForEach) != normText(outer.nb.ForEach) || inner.nb.Iterator != outer.nb.Iterator) {
		return false
	}
	ib, ob := inner.nb.Body, outer.nb.Body
	if inner.kind == effectOther {
		return skip == "" && blockText(inner.nb) == blockText(outer.nb)
	}
	if !dynamicCover(ib, ob) {
		return false
	}
	names := map[string]bool{}
	for _, a := range slices.Concat(ib.Attributes, ob.Attributes) {
		names[a.Name] = true
	}
	for n := range names {
		if n == "effect" || n == skip || (n == "sid" && !merge) {
			continue
		}
		i, o := bodyAttr(ib, n), bodyAttr(ob, n)
		if i == nil || o == nil {
			return false
		}
		if !argCovers(n, i.Value, o.Value) {
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
			ok = principalsCover(is[t], os[t])
		case "condition":
			ok = conditionsCover(is[t], os[t])
		default:
			ok = sameBlocks(is[t], os[t])
		}
		if !ok {
			return false
		}
	}
	return true
}

// argCovers compares one statement argument present on both sides.
func argCovers(name string, i, o Expr) bool {
	if normExpr(i.Text) == normExpr(o.Text) {
		return true
	}
	switch name {
	case "actions", "resources":
		return widens(i, o)
	case "not_actions", "not_resources":
		s, ok := stringSet(o)
		return ok && len(s) > 0 && widens(o, i)
	}
	return false
}

// principalsCover: both sides have principals or neither has, and each
// inner principals block has an outer one of the same type whose
// identifiers are equal or a literal superset.
func principalsCover(inner, outer []*NestedBlock) bool {
	if (len(inner) == 0) != (len(outer) == 0) {
		return false
	}
	for _, i := range inner {
		if !slices.ContainsFunc(outer, func(o *NestedBlock) bool {
			if len(i.Body.Blocks) > 0 || len(o.Body.Blocks) > 0 || len(i.Body.Attributes) != 2 || len(o.Body.Attributes) != 2 {
				return blockText(i) == blockText(o)
			}
			it, ot := bodyAttr(i.Body, "type"), bodyAttr(o.Body, "type")
			ii, oi := bodyAttr(i.Body, "identifiers"), bodyAttr(o.Body, "identifiers")
			return it != nil && ot != nil && ii != nil && oi != nil && normExpr(it.Value.Text) == normExpr(ot.Value.Text) &&
				(normExpr(ii.Value.Text) == normExpr(oi.Value.Text) || widens(ii.Value, oi.Value))
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
func conditionsCover(inner, outer []*NestedBlock) bool {
	key := conditionKey
	for _, side := range [][]*NestedBlock{inner, outer} {
		seen := map[string]bool{}
		for _, c := range side {
			k, ok := key(c)
			if !ok || seen[k] {
				return sameBlocks(inner, outer)
			}
			seen[k] = true
		}
	}
	return conditionsMatch(inner, outer, key)
}

// conditionKey is the test and variable of a condition block that holds
// exactly test, variable and values, or false for any other shape.
func conditionKey(c *NestedBlock) (string, bool) {
	t, v := bodyAttr(c.Body, "test"), bodyAttr(c.Body, "variable")
	if t == nil || v == nil || bodyAttr(c.Body, "values") == nil || len(c.Body.Attributes) != 3 || len(c.Body.Blocks) > 0 {
		return "", false
	}
	return normExpr(t.Value.Text) + "\x00" + normExpr(v.Value.Text), true
}

func conditionsMatch(inner, outer []*NestedBlock, key func(*NestedBlock) (string, bool)) bool {
	for _, o := range outer {
		ok := slices.ContainsFunc(inner, func(i *NestedBlock) bool {
			ik, _ := key(i)
			ok, _ := key(o)
			if ik != ok {
				return false
			}
			iv, ov := bodyAttr(i.Body, "values"), bodyAttr(o.Body, "values")
			return normExpr(iv.Value.Text) == normExpr(ov.Value.Text) ||
				(widensIn("values", "condition", i.Body, o.Body) && widens(iv.Value, ov.Value))
		})
		if !ok {
			return false
		}
	}
	return true
}

// sameBlocks reports two lists of blocks equal as multisets of text.
func sameBlocks(a, b []*NestedBlock) bool {
	if len(a) != len(b) {
		return false
	}
	ta, tb := make([]string, len(a)), make([]string, len(b))
	for i := range a {
		ta[i], tb[i] = blockText(a[i]), blockText(b[i])
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
func dynamicCover(ib, ob Body) bool {
	id, od := dynamicBlocks(ib), dynamicBlocks(ob)
	if len(id) != len(od) {
		return false
	}
	is, _ := staticBlocks(ib)
	os, _ := staticBlocks(ob)
	static := map[string]bool{}
	for _, c := range slices.Concat(is["condition"], os["condition"]) {
		if k, ok := conditionKey(c); ok {
			static[k] = true
		}
	}
	for n, i := range id {
		o := od[n]
		if i.Type != o.Type || normText(i.ForEach) != normText(o.ForEach) || i.Iterator != o.Iterator {
			return false
		}
		ik, iok := conditionKey(i)
		if iok && static[ik] {
			return blockText(&NestedBlock{Body: ib}) == blockText(&NestedBlock{Body: ob})
		}
		if i.Type != "condition" || !iok {
			if blockText(i) != blockText(o) {
				return false
			}
			continue
		}
		if !conditionsMatch([]*NestedBlock{i}, []*NestedBlock{o}, conditionKey) {
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

func normText(e *Expr) string {
	if e == nil {
		return ""
	}
	return normExpr(e.Text)
}

// blockText is the normalized text of a block: its type, labels, dynamic
// arguments, arguments by name and nested blocks in order.
func blockText(nb *NestedBlock) string {
	parts := []string{nb.Type, strings.Join(nb.Labels, " "), strconv.FormatBool(nb.Dynamic), normText(nb.ForEach), nb.Iterator}
	attrs := slices.Clone(nb.Body.Attributes)
	slices.SortFunc(attrs, func(a, b Attribute) int { return strings.Compare(a.Name, b.Name) })
	for _, a := range attrs {
		parts = append(parts, a.Name+" = "+normExpr(a.Value.Text))
	}
	for i := range nb.Body.Blocks {
		parts = append(parts, "{"+blockText(&nb.Body.Blocks[i])+"}")
	}
	return strings.Join(parts, "\n")
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
// is reported against, of the same static or dynamic form: the one with the same
// literal sid, else the one sharing the most literal actions, the first on
// a tie, else the one at the same path.
func partner(b stmt, hs []stmt) *stmt {
	same := func(h stmt) bool { return h.nb.Dynamic == b.nb.Dynamic }
	if sid, ok := attrString(b.nb.Body, "sid"); ok && sid != "" {
		for i := range hs {
			if s, ok := attrString(hs[i].nb.Body, "sid"); ok && s == sid && same(hs[i]) {
				return &hs[i]
			}
		}
	}
	want := attrSet(b.nb.Body, "actions")
	var best *stmt
	most := 0
	for i := range hs {
		if !same(hs[i]) {
			continue
		}
		n := 0
		for s := range attrSet(hs[i].nb.Body, "actions") {
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
	for i := range hs {
		if hs[i].path == b.path {
			return &hs[i]
		}
	}
	return nil
}

func attrString(b Body, name string) (string, bool) {
	a := bodyAttr(b, name)
	if a == nil {
		return "", false
	}
	return literalArg(a.Value)
}

func attrSet(b Body, name string) map[string]bool {
	a := bodyAttr(b, name)
	if a == nil {
		return nil
	}
	s, _ := stringSet(a.Value)
	return s
}
