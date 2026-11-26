package gates

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// fuzzSeeds are hostile shapes the scanners must survive.
var fuzzSeeds = []string{
	"resource \"aws_s3_bucket\" \"x\" {\n  provisioner \"local-exec\" {}\n}\n",
	"provider \"aws\" {\n  assume_role {\n    role_arn = \"arn:aws:iam::123456789012:role/r\"\n  }\n}\n",
	"x = <<EOF\n${\"}\"}\nEOF\n",
	"a = \"${ { \"x\" = \"}\" } }\"\n/* open",
	"module \"m\" { source = \"../../x\" }\nterraform { required_providers { aws = { source = \"hashicorp/aws\" } } }\n",
	"data \"aws_iam_policy\" \"p\" {\n  arn = \"arn:aws:iam::aws:policy/ReadOnlyAccess\"\n}\n",
	"locals { r = \"eu-west-1\" }\ndata \"aws_x\" \"y\" { region = local.r }\n",
	"{{{{{{{{}}\"\\\n<<-A\n A\r\n",
}

// FuzzMask checks the mask keeps the length and every newline in place.
func FuzzMask(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, ok := mask(s)
		if !ok {
			return
		}
		if len(m) != len(s) {
			t.Fatalf("mask length %d, input %d", len(m), len(s))
		}
		for i := range len(s) {
			if (s[i] == '\n') != (m[i] == '\n') {
				t.Fatalf("newline moved at %d", i)
			}
		}
		for _, b := range []string{blank(s), fillBlocks(s)} {
			if len(b) != len(s) || bytes.Count([]byte(b), []byte("\n")) != bytes.Count([]byte(s), []byte("\n")) {
				t.Fatal("a rewrite moved an offset")
			}
		}
	})
}

var hitLine = regexp.MustCompile(`\A(?:G[0-3]|Xrole|Xdocker) .*:[0-9]+\z`)

// FuzzRun runs every mode over one hostile file beside a module: it must
// not fail, and each line is a hit of a line the file has.
func FuzzRun(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ex := filepath.Join(root, "ex")
		if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(ex, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(ex, "main.tf")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		lines := bytes.Count([]byte(s), []byte("\n")) + 1
		for _, o := range []Options{
			{Root: root, Example: ex}, {Root: root, Runner: true, Providers: []string{"ns/*"}},
			{Root: root, Downloaded: true}, {Root: root, Downloaded: true, Runner: true},
		} {
			o.FileBytes, o.TotalBytes = 1<<20, 1<<22
			out, err := Run([]string{p, p}, o)
			if err != nil {
				t.Fatal(err)
			}
			for l := range bytes.SplitSeq(bytes.TrimSuffix(out, []byte("\n")), []byte("\n")) {
				if len(out) == 0 {
					break
				}
				if !hitLine.Match(l) {
					t.Fatalf("line %q", l)
				}
				var n int
				if _, num, ok := bytes.Cut(l[bytes.LastIndexByte(l, ':'):], []byte(":")); ok {
					n, _ = strconv.Atoi(string(num))
				}
				if n > lines {
					t.Fatalf("line %q past the file's %d lines", l, lines)
				}
			}
		}
	})
}
