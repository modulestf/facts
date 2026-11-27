package facts

import "bytes"

// scanLimits reads src once, byte by byte, in the states of the HCL lexer
// (hclsyntax scan_tokens.rl) that change the bracket nesting: expression,
// quoted string and the template sequences inside one. It returns the
// nesting depth nestingDepth gives the lexer's tokens and a unary operator
// run no shorter than prefixRun's. ok is false when it meets a construct it
// does not follow, a heredoc or a block comment with no end; the caller then
// lexes the file to check the limits. It gives up on a byte over 0x7f
// outside a string or a comment too.
//
// The depth is exact because each opener and closer token of nestingDepth
// starts at a byte the scanner reads in the same state: a brace, bracket or
// parenthesis, a quote, ${ or %{ in a string, and } or ~} ending one.
// Comments are skipped and escapes in a string skip the next byte. The
// unary run counts ! and - in expression state with only spaces and tabs
// between them, as the lexer emits no token for those; a - inside a number
// or a name is counted too, so the run is an upper bound.
//
// tokens is an upper bound of the tokens the lexer gives, EOF included, so
// the run's token budget can be charged without lexing the file. Each token
// the lexer gives holds at least one byte the scanner counts on its own: a
// run of name and number bytes counts one, and one more where a letter
// follows a number, as in 1a; every other byte counts one, except spaces and
// tabs, which count one a run inside a string and none outside one; a
// comment counts two, itself and its line end.
func scanLimits(src []byte) (depth, prefix, tokens int, ok bool) {
	const (
		stateExpr = iota
		stateString
	)
	// stack holds the state to return to at each template sequence end,
	// with the brace count it ends at, as the lexer's fcall and fret do.
	type frame struct{ state, braces int }
	var stack []frame
	state, braces, cur, run := stateExpr, 0, 0, 0
	open := func() {
		cur++
		depth = max(depth, cur)
	}
	closeOne := func() {
		if cur > 0 {
			cur--
		}
	}
	tokens = 1 // EOF
	var w wordRun
	if b := bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")); len(b) < len(src) {
		src, tokens = b, tokens+1
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		tokens += w.next(c, state == stateString)
		if state == stateString {
			switch {
			case c == '\\':
				i++
				tokens++
				w = wordRun{}
			case c == '"':
				closeOne()
				state, stack = stack[len(stack)-1].state, stack[:len(stack)-1]
			case (c == '$' || c == '%') && i+1 < len(src) && src[i+1] == '{':
				open()
				braces++
				stack = append(stack, frame{stateString, braces})
				state = stateExpr
				i++
				tokens++
				if i+1 < len(src) && src[i+1] == '~' {
					i++
					tokens++
				}
			case (c == '$' || c == '%') && i+2 < len(src) && src[i+1] == c && src[i+2] == '{':
				i += 2 // $${ and %%{ are literal text
				tokens += 2
				w = wordRun{}
			}
			continue
		}
		if c >= 0x80 {
			// The lexer's identifier tables take a broken UTF-8 lead byte
			// together with the ASCII byte after it, such as \xcf[, so a
			// bracket there is no token.
			return 0, 0, 0, false
		}
		switch c {
		case '!', '-':
			run++
			prefix = max(prefix, run)
			continue
		case ' ', '\t':
			continue
		}
		run = 0
		switch c {
		case '#':
			i = lineEnd(src, i)
			tokens++ // the line end, which the comment token may not hold
		case '/':
			switch {
			case i+1 < len(src) && src[i+1] == '/':
				i = lineEnd(src, i)
				tokens++
			case i+1 < len(src) && src[i+1] == '*':
				j := bytes.Index(src[i+2:], []byte("*/"))
				if j < 0 {
					return 0, 0, 0, false
				}
				i += 2 + j + 1
				tokens++
			}
		case '<':
			if i+1 < len(src) && src[i+1] == '<' {
				return 0, 0, 0, false
			}
		case '"':
			open()
			stack = append(stack, frame{stateExpr, -1})
			state = stateString
		case '{', '[', '(':
			open()
			if c == '{' {
				braces++
			}
		case ']', ')':
			closeOne()
		case '}', '~':
			if c == '~' {
				if i+1 >= len(src) || src[i+1] != '}' {
					continue
				}
				i++
			}
			closeOne()
			if n := len(stack); n > 0 && stack[n-1].braces == braces {
				state, stack = stack[n-1].state, stack[:n-1]
			}
			braces--
		}
	}
	return depth, prefix, tokens, true
}

// lineEnd returns the index of the newline that ends the line holding i, or
// the last index of src when no newline follows.
func lineEnd(src []byte, i int) int {
	if j := bytes.IndexByte(src[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(src) - 1
}

// wordRun is the byte class scanLimits counts tokens by: 0 for none, 'd'
// in a number, 'w' in a name and ' ' in a run of blanks.
type wordRun struct{ class byte }

// next returns the tokens byte c adds to the upper bound: one where a run
// of name or number bytes starts or a letter follows a number, none where
// the run goes on, one for the first blank of a run inside a string and
// none outside one, and one for any other byte.
func (w *wordRun) next(c byte, inString bool) int {
	switch {
	case c >= '0' && c <= '9':
		if w.class == 'd' || w.class == 'w' {
			return 0
		}
		w.class = 'd'
	case c == '_' || ('a' <= c|0x20 && c|0x20 <= 'z'):
		if w.class == 'w' {
			return 0
		}
		w.class = 'w'
	case c == ' ' || c == '\t':
		blank := w.class == ' '
		w.class = ' '
		if blank || !inString {
			return 0
		}
	default:
		w.class = 0
	}
	return 1
}
