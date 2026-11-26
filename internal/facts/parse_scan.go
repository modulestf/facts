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
func scanLimits(src []byte) (depth, prefix int, ok bool) {
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
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if state == stateString {
			switch {
			case c == '\\':
				i++
			case c == '"':
				closeOne()
				state, stack = stack[len(stack)-1].state, stack[:len(stack)-1]
			case (c == '$' || c == '%') && i+1 < len(src) && src[i+1] == '{':
				open()
				braces++
				stack = append(stack, frame{stateString, braces})
				state = stateExpr
				i++
				if i+1 < len(src) && src[i+1] == '~' {
					i++
				}
			case (c == '$' || c == '%') && i+2 < len(src) && src[i+1] == c && src[i+2] == '{':
				i += 2 // $${ and %%{ are literal text
			}
			continue
		}
		if c >= 0x80 {
			// The lexer's identifier tables take a broken UTF-8 lead byte
			// together with the ASCII byte after it, such as \xcf[, so a
			// bracket there is no token.
			return 0, 0, false
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
		case '/':
			switch {
			case i+1 < len(src) && src[i+1] == '/':
				i = lineEnd(src, i)
			case i+1 < len(src) && src[i+1] == '*':
				j := bytes.Index(src[i+2:], []byte("*/"))
				if j < 0 {
					return 0, 0, false
				}
				i += 2 + j + 1
			}
		case '<':
			if i+1 < len(src) && src[i+1] == '<' {
				return 0, 0, false
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
	return depth, prefix, true
}

// lineEnd returns the index of the newline that ends the line holding i, or
// the last index of src when no newline follows.
func lineEnd(src []byte, i int) int {
	if j := bytes.IndexByte(src[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(src) - 1
}
