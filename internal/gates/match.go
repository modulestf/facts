package gates

import "strings"

// The patterns of the gates, one function each. A find function returns the
// leftmost match at or after from, as Perl's m//g does, with the offsets the
// rules read; a negative start means no match.

// span is a match: its start and end, and the group offsets a rule reads.
type span struct{ start, end, g0, g1 int }

var none = span{start: -1}

// findFrom tries at every index from from on.
func findFrom(s string, from int, at func(string, int) (span, bool)) span {
	for i := from; i <= len(s); i++ {
		if m, ok := at(s, i); ok {
			return m
		}
	}
	return none
}

// findWord is findFrom for a pattern that starts with the literal w: only
// the indexes where w is are tried.
func findWord(s string, from int, w string, at func(string, int) (span, bool)) span {
	for i := from; i <= len(s); {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return none
		}
		if m, ok := at(s, i+j); ok {
			return m
		}
		i += j + 1
	}
	return none
}

// adjKeywords are the words $ADJ refuses right against a quote.
var adjKeywords = []string{"resource", "data", "provider", "module", "variable", "output", "locals",
	"terraform", "moved", "removed", "ephemeral", "check", "import", "action", "backend", "cloud",
	"provisioner", "connection", "lifecycle", "dynamic", "required_providers"}

// findADJ is the first match of $ADJ: \b(?:<keyword>)".
func findADJ(s string) int {
	for i := 0; i < len(s); i++ {
		if !isIdentStart(s[i]) || !boundary(s, i) {
			continue
		}
		for _, k := range adjKeywords {
			if hasAt(s, i, k+`"`) {
				return i
			}
		}
	}
	return -1
}

// kwKeywords are the block words of $KW.
var kwKeywords = []string{"resource", "data", "provider", "module", "moved", "removed", "ephemeral",
	"check", "import", "action", "backend", "cloud", "provisioner", "dynamic"}

// atKW is $KW at i: a block word, a bare label, and then quoted or bare
// labels up to an opening brace:
// \b(?:...)\s+[A-Za-z_][\w-]*(?:\s*"[^"]*"|\s+[A-Za-z_][\w-]*)*\s*\{.
func atKW(s string, i int) (span, bool) {
	if i >= len(s) || !isIdentStart(s[i]) || !boundary(s, i) {
		return none, false
	}
	for _, k := range kwKeywords {
		if !hasAt(s, i, k) {
			continue
		}
		j := i + len(k)
		k2 := skipSpace(s, j)
		if k2 == j {
			return none, false
		}
		j = identEnd(s, k2)
		if j < 0 {
			return none, false
		}
		for {
			k3 := skipSpace(s, j)
			if k3 < len(s) && s[k3] == '"' {
				e := quoteEnd(s, k3)
				if e < 0 {
					return none, false
				}
				j = e
				continue
			}
			if k3 < len(s) && s[k3] == '{' {
				return span{start: i, end: k3 + 1}, true
			}
			if k3 > j {
				if e := identEnd(s, k3); e >= 0 {
					j = e
					continue
				}
			}
			return none, false
		}
	}
	return none, false
}

// atHeader is \b<word>\s+"[^"]*"\s* at i, the header of a provider or
// module block. g0 and g1 are the label with its quotes.
func atHeader(word string) func(string, int) (span, bool) {
	return func(s string, i int) (span, bool) {
		if !hasAt(s, i, word) || !boundary(s, i) {
			return none, false
		}
		j := i + len(word)
		k := skipSpace(s, j)
		if k == j {
			return none, false
		}
		e := quoteEnd(s, k)
		if e < 0 {
			return none, false
		}
		return span{start: i, end: skipSpace(s, e), g0: k, g1: e}, true
	}
}

// atWordSpace is \b<word>\s* at i.
func atWordSpace(word string) func(string, int) (span, bool) {
	return func(s string, i int) (span, bool) {
		if !hasAt(s, i, word) || !boundary(s, i) {
			return none, false
		}
		return span{start: i, end: skipSpace(s, i+len(word))}, true
	}
}

// atLocals is \blocals\s*(?=\{) at i; its end is the brace.
func atLocals(s string, i int) (span, bool) {
	m, ok := atWordSpace("locals")(s, i)
	if !ok || m.end >= len(s) || s[m.end] != '{' {
		return none, false
	}
	return m, true
}

// atBlockHeader is \b<kw>((?:\s*"[^"\n]*")+)\s*(?=\{) at i: g0 and g1 are
// the group, its end the brace.
func atBlockHeader(kw string) func(string, int) (span, bool) {
	return func(s string, i int) (span, bool) {
		if !hasAt(s, i, kw) || !boundary(s, i) {
			return none, false
		}
		g0 := i + len(kw)
		j, n := g0, 0
		for {
			k := skipSpace(s, j)
			e := quoteLineEnd(s, k)
			if e < 0 {
				break
			}
			j, n = e, n+1
		}
		if n == 0 {
			return none, false
		}
		k := skipSpace(s, j)
		if k >= len(s) || s[k] != '{' {
			return none, false
		}
		return span{start: i, end: k, g0: g0, g1: j}, true
	}
}

// atLabelLine is ^[ \t]*[A-Za-z_][\w-]*((?:[ \t]+"[^"\n]*")+)[ \t]*\{ at a
// line start i; g0 and g1 are the labels.
func atLabelLine(s string, i int) (span, bool) {
	j := identEnd(s, skipBlank(s, i))
	if j < 0 {
		return none, false
	}
	g0, n := j, 0
	for {
		k := skipBlank(s, j)
		if k == j {
			break
		}
		e := quoteLineEnd(s, k)
		if e < 0 {
			break
		}
		j, n = e, n+1
	}
	if n == 0 {
		return none, false
	}
	k := skipBlank(s, j)
	if k >= len(s) || s[k] != '{' {
		return none, false
	}
	return span{start: i, end: k + 1, g0: g0, g1: j}, true
}

// findLines is findFrom over the line starts at or after from only.
func findLines(s string, from int, at func(string, int) (span, bool)) span {
	for i := lineStartFrom(s, from); i >= 0 && i <= len(s); i = nextLineStart(s, i) {
		if m, ok := at(s, i); ok {
			return m
		}
		if i == len(s) {
			break
		}
	}
	return none
}

// findSpaceLines is findLines for a pattern that opens with ^\s*: a line
// start inside the same run of space reaches the same text, so it fails
// the same way and is skipped.
func findSpaceLines(s string, from int, at func(string, int) (span, bool)) span {
	for i := lineStartFrom(s, from); i >= 0 && i <= len(s); {
		if m, ok := at(s, i); ok {
			return m
		}
		k := skipSpace(s, i)
		if k >= len(s) {
			break
		}
		i = nextLineStart(s, k)
	}
	return none
}

// atTopLine is ^([A-Za-z_][\w-]*)(?:\s+"[^"]*")*\s*\{ at a line start i;
// g0 and g1 are the word.
func atTopLine(s string, i int) (span, bool) {
	j := identEnd(s, i)
	if j < 0 {
		return none, false
	}
	return labelsToBrace(s, i, j, span{g0: i, g1: j})
}

// labelsToBrace reads (?:\s+"[^"]*")*\s*\{ from j.
func labelsToBrace(s string, start, j int, m span) (span, bool) {
	for {
		k := skipSpace(s, j)
		if k > j && k < len(s) && s[k] == '"' {
			if e := quoteEnd(s, k); e >= 0 {
				j = e
				continue
			}
			return none, false
		}
		if k < len(s) && s[k] == '{' {
			m.start, m.end = start, k+1
			return m, true
		}
		return none, false
	}
}

var newBlockWords = []string{"ephemeral", "check", "import", "action"}

// atNewBlock is ^\s*(?:ephemeral|check|import|action)\b(?:\s+"[^"]*")*\s*\{
// at a line start i.
func atNewBlock(s string, i int) (span, bool) {
	k := skipSpace(s, i)
	for _, w := range newBlockWords {
		if hasAt(s, k, w) && boundary(s, k+len(w)) {
			return labelsToBrace(s, i, k+len(w), span{})
		}
	}
	return none, false
}

// atTypeRef is \b(resource|data)\s+"([^"]*)" at i: g0 and g1 are the type.
func atTypeRef(s string, i int) (span, bool) {
	if !boundary(s, i) {
		return none, false
	}
	var w string
	switch {
	case hasAt(s, i, "resource"):
		w = "resource"
	case hasAt(s, i, "data"):
		w = "data"
	default:
		return none, false
	}
	j := i + len(w)
	k := skipSpace(s, j)
	if k == j {
		return none, false
	}
	e := quoteEnd(s, k)
	if e < 0 {
		return none, false
	}
	return span{start: i, end: e, g0: k + 1, g1: e - 1}, true
}

// atCode is the laptop code-execution pattern at i:
// (?:provisioner|\bfile\w*\s*\(|templatefile\s*\(|provider::|\bcloud\s*\{|backend\s+").
func atCode(s string, i int) (span, bool) {
	if hasAt(s, i, "provisioner") {
		return span{start: i, end: i + len("provisioner")}, true
	}
	if hasAt(s, i, "file") && boundary(s, i) {
		j := i + 4
		for j < len(s) && isWord(s[j]) {
			j++
		}
		if k := skipSpace(s, j); k < len(s) && s[k] == '(' {
			return span{start: i, end: k + 1}, true
		}
	}
	if hasAt(s, i, "templatefile") {
		if k := skipSpace(s, i+len("templatefile")); k < len(s) && s[k] == '(' {
			return span{start: i, end: k + 1}, true
		}
	}
	if hasAt(s, i, "provider::") {
		return span{start: i, end: i + len("provider::")}, true
	}
	return atCloudBackend(s, i)
}

// atCloudBackend is (?:\bcloud\s*\{|backend\s+") at i.
func atCloudBackend(s string, i int) (span, bool) {
	if hasAt(s, i, "cloud") && boundary(s, i) {
		if k := skipSpace(s, i+5); k < len(s) && s[k] == '{' {
			return span{start: i, end: k + 1}, true
		}
	}
	if hasAt(s, i, "backend") {
		j := i + 7
		if k := skipSpace(s, j); k > j && k < len(s) && s[k] == '"' {
			return span{start: i, end: k + 1}, true
		}
	}
	return none, false
}

// atProviderEntry is ^\s*([A-Za-z_][\w-]*)\s*=\s*($B|"[^"]*") at a line
// start i of t: g0 and g1 are the value, and the name is s[name0:name1].
func atProviderEntry(t *text) func(string, int) (span, bool) {
	return func(s string, i int) (span, bool) {
		k := skipSpace(s, i)
		ne := identEnd(s, k)
		if ne < 0 {
			return none, false
		}
		eq := skipSpace(s, ne)
		if eq >= len(s) || s[eq] != '=' {
			return none, false
		}
		v := skipSpace(s, eq+1)
		e := t.block(v)
		if e < 0 {
			e = quoteEnd(s, v)
		}
		if e < 0 {
			return none, false
		}
		// start holds the name's start; end is the match end, as m//g sets pos.
		return span{start: k, end: e, g0: v, g1: e}, true
	}
}

// atSourceAttr is \bsource\s*=\s*" at i; end is after the quote.
func atSourceAttr(s string, i int) (span, bool) {
	if !hasAt(s, i, "source") || !boundary(s, i) {
		return none, false
	}
	k := skipSpace(s, i+6)
	if k >= len(s) || s[k] != '=' {
		return none, false
	}
	q := skipSpace(s, k+1)
	if q >= len(s) || s[q] != '"' {
		return none, false
	}
	return span{start: i, end: q + 1}, true
}

// sourceAfter is the source value $s =~ /\A([^"]*)"/ reads at i, or "" with
// false when no quote ends it.
func sourceAfter(s string, i int) (string, bool) {
	if i > len(s) {
		return "", false
	}
	j := strings.IndexByte(s[i:], '"')
	if j < 0 {
		return "", false
	}
	return s[i : i+j], true
}

// atDefaultTags is \bdefault_tags\s*$B at i.
func atDefaultTags(t *text) func(string, int) (span, bool) {
	return func(s string, i int) (span, bool) {
		m, ok := atWordSpace("default_tags")(s, i)
		if !ok {
			return none, false
		}
		e := t.block(m.end)
		if e < 0 {
			return none, false
		}
		return span{start: i, end: e}, true
	}
}

// replaceAll is s///g with at as the pattern: each match, leftmost first,
// replaced by what with gives.
func replaceAll(s, w string, at func(string, int) (span, bool), with func(string) string) string {
	var b strings.Builder
	last := 0
	for from := 0; from <= len(s); {
		m := findWord(s, from, w, at)
		if m.start < 0 {
			break
		}
		b.WriteString(s[last:m.start])
		b.WriteString(with(s[m.start:m.end]))
		last, from = m.end, m.end
	}
	b.WriteString(s[last:])
	return b.String()
}
