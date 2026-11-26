package facts

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// padTF is a valid .tf file of about n bytes: one variable and comment lines.
func padTF(n int) string {
	line := "# " + strings.Repeat("x", 78) + "\n"
	return "variable \"v\" {}\n" + strings.Repeat(line, n/len(line))
}

// denseTF is a valid .tf file with many tokens: one attribute per line.
func denseTF(attrs int) string {
	var b strings.Builder
	b.WriteString("locals {\n")
	for i := range attrs {
		fmt.Fprintf(&b, "  a%d = 1\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

func TestFileSizeCap(t *testing.T) {
	t.Parallel()
	if DefaultLimits.MaxFileBytes != 2<<20 || DefaultLimits.MaxTokens != 262144 {
		t.Fatalf("limits are %d bytes and %d tokens", DefaultLimits.MaxFileBytes, DefaultLimits.MaxTokens)
	}
	mod := func(t *testing.T, files map[string]string) *Module {
		t.Helper()
		m, err := ParseDir(mustTree(t, writeTree(t, files)), ".", DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	t.Run("a file between 1 and 2 MiB parses", func(t *testing.T) {
		t.Parallel()
		src := padTF(3 << 19) // 1.5 MiB
		m := mod(t, map[string]string{"main.tf": src})
		if len(src) <= 1<<20 || len(src) > 2<<20 {
			t.Fatalf("test file is %d bytes", len(src))
		}
		if len(m.Unsupported) != 0 {
			t.Errorf("unsupported: %q", m.Unsupported)
		}
	})
	t.Run("a file over 2 MiB is refused with the size wording", func(t *testing.T) {
		t.Parallel()
		m := mod(t, map[string]string{"main.tf": padTF(2<<20 + 200)})
		if len(m.Unsupported) != 1 || !strings.Contains(m.Unsupported[0], "main.tf is over the file size limit") || m.reasonCode() != ReasonLimitExceeded {
			t.Errorf("unsupported %q, code %q", m.Unsupported, m.reasonCode())
		}
	})
	t.Run("a link still says link", func(t *testing.T) {
		t.Parallel()
		m := mod(t, map[string]string{"real.txt": "x", "main.tf": "->real.txt"})
		if len(m.Unsupported) != 1 || !strings.Contains(m.Unsupported[0], "main.tf is a symbolic link") || m.reasonCode() != ReasonSymlink {
			t.Errorf("unsupported %q, code %q", m.Unsupported, m.reasonCode())
		}
	})
	t.Run("a file past the token cap is refused with the limit code", func(t *testing.T) {
		t.Parallel()
		m := mod(t, map[string]string{"main.tf": denseTF(70000)}) // 4 or more tokens a line
		if len(m.Unsupported) != 1 || !strings.Contains(m.Unsupported[0], "tokens over the limit 262144") || m.reasonCode() != ReasonLimitExceeded {
			t.Errorf("unsupported %q, code %q", m.Unsupported, m.reasonCode())
		}
	})
	t.Run("a dense file under both caps parses within a time bound", func(t *testing.T) {
		t.Parallel()
		src := denseTF(65000) // about 260000 tokens, 455 KB
		start := time.Now()
		m := mod(t, map[string]string{"main.tf": src})
		if d := time.Since(start); d > 30*time.Second {
			t.Errorf("parse took %s", d)
		}
		if len(m.Unsupported) != 0 {
			t.Errorf("unsupported: %q", m.Unsupported)
		}
	})
}

// BenchmarkParseCapFile parses a file at the file size cap, and one near the
// token cap, so a change of either cap shows its time and memory.
func BenchmarkParseCapFile(b *testing.B) {
	for name, src := range map[string]string{
		"2MiB-comments": padTF(2<<20 - 100),
		"262k-tokens":   denseTF(65000),
	} {
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := ParseBody("main.tf", []byte(src), DefaultLimits); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
