package main

import (
	"fmt"
	"strings"
)

// oneLine escapes the control characters of s (every byte below 0x20 and
// 0x7f, a line break and ESC among them) as \xNN, so text from an input tree
// or a command line cannot end the stderr line it is printed on or start a
// forged one. Every message that carries such text goes through it before it
// is written; the quoting at the error sites stays as defence in depth.
func oneLine(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "\\x%02x", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}
