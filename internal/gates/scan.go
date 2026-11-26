package gates

import "strings"

// The text helpers of the gates. They read bytes as Perl reads a string it
// never decoded: \w, \s and \b are ASCII, and a byte over 0x7f is no letter
// and no space. Every helper keeps the length of the text it rewrites and
// its newlines in place, so an offset in a rewritten text is an offset in
// the file and a line number counts the same newlines.

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// isIdent is [\w-].
func isIdent(c byte) bool { return isWord(c) || c == '-' }

// isSpace is Perl's \s: space, tab, newline, carriage return, form feed and
// vertical tab.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' }

// boundary is \b before s[i].
func boundary(s string, i int) bool {
	before := i > 0 && isWord(s[i-1])
	after := i < len(s) && isWord(s[i])
	return before != after
}

// skipSpace returns the first index at or after i that is no \s.
func skipSpace(s string, i int) int {
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

func skipBlank(s string, i int) int {
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return i
}

// identEnd returns the end of [A-Za-z_][\w-]* at i, or -1.
func identEnd(s string, i int) int {
	if i >= len(s) || !isIdentStart(s[i]) {
		return -1
	}
	i++
	for i < len(s) && isIdent(s[i]) {
		i++
	}
	return i
}

func hasAt(s string, i int, w string) bool { return i >= 0 && i <= len(s) && strings.HasPrefix(s[i:], w) }

// lineEnd is the index of the newline at or after i, or len(s).
func lineEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(s)
}

// nextLineStart is the first index after i where /^/m matches.
func nextLineStart(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return -1
}

// lineStartFrom is the first index at or after i where /^/m matches.
func lineStartFrom(s string, i int) int {
	if i == 0 || i <= len(s) && s[i-1] == '\n' {
		return i
	}
	return nextLineStart(s, i)
}

// fill replaces every byte but a newline with a space.
func fill(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

// firstNonASCII is the offset of the first byte over 0x7f, or -1.
func firstNonASCII(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return i
		}
	}
	return -1
}

// quoteEnd returns the index after the closing quote of "[^"]*" opening at
// i, or -1.
func quoteEnd(s string, i int) int {
	if i >= len(s) || s[i] != '"' {
		return -1
	}
	if j := strings.IndexByte(s[i+1:], '"'); j >= 0 {
		return i + 1 + j + 1
	}
	return -1
}

// quoteLineEnd is quoteEnd for "[^"\n]*".
func quoteLineEnd(s string, i int) int {
	if i >= len(s) || s[i] != '"' {
		return -1
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '"':
			return j + 1
		case '\n':
			return -1
		}
	}
	return -1
}

// firstQuoted is the first match of /"([^"]*)"/ in s: its content.
func firstQuoted(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	e := quoteEnd(s, i)
	if e < 0 {
		return "", false
	}
	return s[i+1 : e-1], true
}

// quotedLabels is every match of /"([^"\n]*)"/g in s.
func quotedLabels(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if s[i] != '"' {
			i++
			continue
		}
		if e := quoteLineEnd(s, i); e >= 0 {
			out = append(out, s[i+1:e-1])
			i = e
			continue
		}
		i++
	}
	return out
}

// text is a string with its brace pairs, found once.
type text struct {
	s    string
	pair []int32 // the index of the closing brace of each opening one, -1 when none
}

func newText(s string) *text { return &text{s: s} }

// block is the end, after the closing brace, of the balanced block that
// opens at i: Perl's (?<b>\{(?:[^{}]++|(?&b))*\}). It is -1 when s[i] is no
// opening brace or the block does not close.
func (t *text) block(i int) int {
	if i < 0 || i >= len(t.s) || t.s[i] != '{' {
		return -1
	}
	if t.pair == nil {
		t.pair = make([]int32, len(t.s))
		var stack []int32
		for j := 0; j < len(t.s); j++ {
			t.pair[j] = -1
			switch t.s[j] {
			case '{':
				stack = append(stack, int32(j))
			case '}':
				if n := len(stack); n > 0 {
					t.pair[stack[n-1]] = int32(j)
					stack = stack[:n-1]
				}
			}
		}
	}
	if e := t.pair[i]; e >= 0 {
		return int(e) + 1
	}
	return -1
}

// fillBlocks is s with every balanced block, leftmost first and not
// overlapping, filled: s/$B/fill($&)/ge.
func fillBlocks(s string) string {
	t := newText(s)
	b := []byte(s)
	for i := 0; i < len(s); {
		if e := t.block(i); e >= 0 {
			for j := i; j < e; j++ {
				if b[j] != '\n' {
					b[j] = ' '
				}
			}
			i = e
			continue
		}
		i++
	}
	return string(b)
}

// blank fills the content of every "..." string, newlines aside, with x:
// s{"((?:[^"\\\n]|\\.)*)"}{...}ge.
func blank(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		if b[i] != '"' {
			i++
			continue
		}
		j := i + 1
		for j < len(b) {
			c := b[j]
			if c == '"' {
				break
			}
			if c == '\n' {
				j = len(b)
				break
			}
			if c == '\\' {
				if j+1 < len(b) && b[j+1] != '\n' {
					j += 2
					continue
				}
				j = len(b)
				break
			}
			j++
		}
		if j >= len(b) {
			i++
			continue
		}
		for k := i + 1; k < j; k++ {
			b[k] = 'x'
		}
		i = j + 1
	}
	return string(b)
}

// stripComments deletes each # or // comment to the end of its line:
// s{(?:#|//)[^\n]*}{}g. It is the one helper that moves offsets; it keeps
// every newline, so line numbers stay.
func stripComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '#' || s[i] == '/' && i+1 < len(s) && s[i+1] == '/' {
			i = lineEnd(s, i)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// skipString reads the string whose opening quote is at p, with its
// template sequences, as the Perl's skip_string does, and returns the index
// after its closing quote, or -1 when it does not end.
func skipString(s string, p int) int {
	const str = -1
	i := p + 1
	stack := []int{str}
	for len(stack) > 0 {
		top := len(stack) - 1
		if stack[top] == str {
			if i+1 < len(s) && (s[i] == '$' || s[i] == '%') && s[i+1] == '{' {
				i += 2
				stack = append(stack, 0)
				continue
			}
			if i >= len(s) {
				return -1
			}
			switch c := s[i]; {
			case c == '\\':
				if i+1 < len(s) && s[i+1] != '\n' {
					i += 2
					continue
				}
				return -1
			case (c == '$' || c == '%') && i+2 < len(s) && s[i+1] == c && s[i+2] == '{':
				i += 3
			case c != '"' && c != '$' && c != '%' && c != '\n':
				for i < len(s) && s[i] != '"' && s[i] != '\\' && s[i] != '$' && s[i] != '%' && s[i] != '\n' {
					i++
				}
			case c == '$' || c == '%':
				i++
			case c == '"':
				i++
				stack = stack[:top]
			default:
				return -1
			}
			continue
		}
		if i >= len(s) {
			return -1
		}
		switch s[i] {
		case '"':
			stack = append(stack, str)
			i++
		case '{':
			stack[top]++
			i++
		case '}':
			if stack[top] > 0 {
				stack[top]--
			} else {
				stack = stack[:top]
			}
			i++
		default:
			for i < len(s) && s[i] != '"' && s[i] != '{' && s[i] != '}' {
				i++
			}
		}
	}
	return i
}

// mask returns s with every string, comment and heredoc filled, the opening
// quote, closing quote and heredoc << kept, or false when one does not end.
func mask(s string) (string, bool) {
	o := make([]byte, 0, len(s))
	for p := 0; p < len(s); {
		c := s[p]
		if c == '#' || c == '/' && p+1 < len(s) && s[p+1] == '/' {
			e := lineEnd(s, p)
			o = append(o, fill(s[p:e])...)
			p = e
			continue
		}
		if c == '/' && p+1 < len(s) && s[p+1] == '*' {
			j := strings.Index(s[p+2:], "*/")
			if j < 0 {
				return "", false
			}
			e := p + 2 + j + 2
			o = append(o, fill(s[p:e])...)
			p = e
			continue
		}
		if c == '<' {
			if e, ok, isDoc := heredoc(s, p); isDoc {
				if !ok {
					return "", false
				}
				o = append(o, "<<"...)
				o = append(o, fill(s[p+2:e])...)
				p = e
				continue
			}
		}
		if c == '"' {
			e := skipString(s, p)
			if e < 0 {
				return "", false
			}
			o = append(o, '"')
			o = append(o, fill(s[p+1:e-1])...)
			o = append(o, '"')
			p = e
			continue
		}
		o = append(o, c)
		p++
	}
	return string(o), true
}

// heredoc reads a heredoc whose opener <<-?ID[ \t]*\n is at p. isDoc is
// false when there is no opener; ok is false when the heredoc does not end,
// or a body line would end it once Terraform trims it. e is the end of the
// closing line, before its newline.
func heredoc(s string, p int) (e int, ok, isDoc bool) {
	if !hasAt(s, p, "<<") {
		return 0, false, false
	}
	i := p + 2
	if i < len(s) && s[i] == '-' {
		i++
	}
	ie := identEnd(s, i)
	if ie < 0 {
		return 0, false, false
	}
	id := s[i:ie]
	j := skipBlank(s, ie)
	if j >= len(s) || s[j] != '\n' {
		return 0, false, false
	}
	q := j + 1
	for k := q; k >= 0 && k <= len(s); k = nextLineStart(s, k) {
		a := skipBlank(s, k)
		if hasAt(s, a, id) {
			b := skipBlank(s, a+len(id))
			if b == len(s) || s[b] == '\n' {
				for _, l := range perlSplitLines(s[q:k]) {
					if squeeze(l) == id {
						return 0, false, true
					}
				}
				return b, true, true
			}
		}
		if k == len(s) {
			break
		}
	}
	return 0, false, true
}

// squeeze drops every \s byte and every byte over 0x7f.
func squeeze(l string) string {
	b := make([]byte, 0, len(l))
	for i := 0; i < len(l); i++ {
		if !isSpace(l[i]) && l[i] <= 0x7f {
			b = append(b, l[i])
		}
	}
	return string(b)
}

// perlSplitLines is split /\n/, s: trailing empty fields dropped.
func perlSplitLines(s string) []string {
	f := strings.Split(s, "\n")
	for len(f) > 0 && f[len(f)-1] == "" {
		f = f[:len(f)-1]
	}
	return f
}

// lineCount is the hit's line: 1 and the newlines before p.
func lineCount(s string, p int) int { return 1 + strings.Count(s[:p], "\n") }
