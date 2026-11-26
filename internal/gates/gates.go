// Package gates runs the plan pass's gates G0 to G3 over a list of .tf
// files, before Terraform runs on them with credentials, and prints one
// line per hit: "<gate> <path>:<line>". G0 is a file the gates cannot read
// as text, G1 a provider, G2 a module source and G3 a block or expression
// that runs code or reads an account; in runner mode Xrole and Xdocker are
// the exceptions a runner records.
//
// It is a port of the plan pass's Perl, line for line: the rules read text
// with the Perl's patterns, hits in comments and strings included, so its
// output is the Perl's byte for byte. It reads only the listed files and
// the names in their directories.
package gates

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/readcap"
)

// Unreadable is the line of the file list that stands for a file the
// caller could not list; it is a G0 hit.
const Unreadable = "UNREADABLE"

// ErrDeadline is returned when a run passes Options.Deadline.
var ErrDeadline = errors.New("deadline")

// Options of one run.
type Options struct {
	Downloaded bool     // the files are of downloaded modules, not of the clone
	Runner     bool     // runner mode: the code-execution gates relaxed
	Root       string   // the clone, which local module sources must stay in
	Example    string   // the example root the laptop event bus and region rules need; may be empty
	Providers  []string // runner mode: more allowed providers, ns/name or ns/*
	FileBytes  int64    // a larger file reads as unreadable
	TotalBytes int64    // bytes read in all
	Deadline   time.Time
}

// tok is a value as the laptop rules read it: kind str, null, bool, var,
// local or other, and its text.
type tok struct{ kind, val string }

func lit(t *tok) bool { return t != nil && (t.kind == "str" || t.kind == "null" || t.kind == "bool") }

type file struct {
	path string
	raw  string
	ok   bool // read; false is a G0 hit
}

// run is the state of one run, the Perl's globals.
type run struct {
	o    Options
	root string
	ex   string
	hasEx bool
	out  bytes.Buffer
	prov provSet

	// laptop mode
	masks map[string]*string
	calls []*call
	vars  map[string]map[string]*variable
	dirs  map[string]bool
	loc   map[string]map[string]*local
	lbad  map[string]bool
	ban, a3, lall bool
}

type call struct {
	from, to string
	attrs    map[string]*tok
	dup      map[string]bool
}

type variable struct {
	def, nullable *tok
	dup           bool
}

type local struct {
	n int
	t *tok
}

// Run checks the files at paths and returns the hit lines.
func Run(paths []string, o Options) ([]byte, error) {
	r := &run{o: o}
	root, ok := realpath(o.Root)
	if !ok {
		return nil, errors.New("root does not resolve")
	}
	r.root = root
	if o.Example != "" {
		r.ex, r.hasEx = realpath(o.Example)
	}
	p, err := newProvSet(o.Providers)
	if err != nil {
		return nil, err
	}
	r.prov = p
	files, err := readAll(paths, o)
	if err != nil {
		return nil, err
	}
	if !o.Runner {
		r.prepare(files)
	}
	for i := range files {
		if !o.Deadline.IsZero() && time.Now().After(o.Deadline) {
			return nil, ErrDeadline
		}
		r.check(&files[i])
	}
	return r.out.Bytes(), nil
}

// readAll reads each listed file, following links as Perl's open does.
func readAll(paths []string, o Options) ([]file, error) {
	files := make([]file, len(paths))
	var total int64
	for i, p := range paths {
		files[i].path = p
		if p == Unreadable {
			continue
		}
		rp, ok := realpath(p)
		if !ok {
			continue
		}
		data, err := readcap.File(rp, o.FileBytes)
		if err != nil {
			continue
		}
		total += int64(len(data))
		if total > o.TotalBytes {
			return nil, fmt.Errorf("files over %d bytes in all", o.TotalBytes)
		}
		files[i].raw, files[i].ok = string(data), true
	}
	return files, nil
}

// hit prints one hit. With a text and an offset its line is the newlines
// before the offset; without, it is 0.
func (r *run) hit(g, f string, t string, p int) {
	fmt.Fprintf(&r.out, "%s %s:%d\n", g, f, lineCount(t, p))
}

func (r *run) hitFile(g, f string) { fmt.Fprintf(&r.out, "%s %s:0\n", g, f) }

// dirOf drops the last /component of f, as s{/[^/]*\z}{} does; a path with
// no slash is its own directory.
func dirOf(f string) string {
	if d, _, ok := strings.CutLast(f, "/"); ok {
		return d
	}
	return f
}

func baseOf(f string) string { return f[strings.LastIndexByte(f, '/')+1:] }

// overrideName is m{(?:\A|/)(?:[^/]*_)?override\.tf\z}.
func overrideName(f string) bool {
	b := baseOf(f)
	return b == "override.tf" || strings.HasSuffix(b, "_override.tf")
}

// banName is the laptop BAN pattern: a name that turns the relaxations off.
func banName(n string) bool {
	return n == "override.tf" || strings.HasSuffix(n, "_override.tf") || strings.HasSuffix(n, ".tfvars") ||
		strings.HasSuffix(n, ".tfvars.json") || strings.Contains(n, ".auto.tfvars") || strings.HasSuffix(n, ".tf.json")
}

// under is index("$r/", "$root/") == 0.
func under(p, root string) bool { return strings.HasPrefix(p+"/", root+"/") }

func localSource(s string) bool { return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") }

// canon is the Perl's canon: lower case, and a two-part source gets the
// public registry's host.
func canon(s string) string {
	s = asciiLower(s)
	if strings.Count(s, "/") == 1 {
		s = "registry.terraform.io/" + s
	}
	return s
}

// asciiLower is Perl's lc of a byte string: A-Z only.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// lastPart is the text after the last slash.
func lastPart(s string) string { return s[strings.LastIndexByte(s, '/')+1:] }

// laptopProviders are the providers a laptop run allows.
var laptopProviders = map[string]bool{
	"registry.terraform.io/hashicorp/aws": true, "registry.terraform.io/hashicorp/random": true,
	"registry.terraform.io/hashicorp/time": true, "registry.terraform.io/hashicorp/tls": true,
	"registry.terraform.io/hashicorp/cloudinit": true,
}

// moduleSource is $reg: a terraform-aws-modules registry source, with an
// optional submodule.
func moduleSource(s string) bool {
	s = strings.TrimPrefix(s, "registry.terraform.io/")
	rest, ok := strings.CutPrefix(s, "terraform-aws-modules/")
	if !ok {
		return false
	}
	i := strings.IndexByte(rest, '/')
	if i <= 0 || !allIn(rest[:i], "-") {
		return false
	}
	rest = rest[i+1:]
	if rest == "aws" {
		return true
	}
	sub, ok := strings.CutPrefix(rest, "aws//modules/")
	return ok && sub != "" && allIn(sub, "_-")
}

// allIn is s =~ /\A[a-z0-9<extra>]+\z/ for a non-empty s.
func allIn(s, extra string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && strings.IndexByte(extra, c) < 0 {
			return false
		}
	}
	return s != ""
}

// githubSource is $gh, a source on github.com, which runner mode allows.
func githubSource(s string) bool {
	for _, p := range []string{"github.com/", "git@github.com:", "git::https://github.com/", "git::ssh://git@github.com/"} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// isDir is Perl's -d: the path, links followed, is a directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// check runs the gates of one file.
func (r *run) check(f *file) {
	if !f.ok {
		r.hitFile("G0", f.path)
		return
	}
	if overrideName(f.path) {
		r.hitFile("G0", f.path)
	}
	if i := strings.IndexAny(f.raw, "\r\f\v"); i >= 0 {
		r.hit("G0", f.path, f.raw, i)
	}
	if r.o.Runner {
		r.runnerFile(f.path, f.raw)
		return
	}
	r.laptopFile(f.path, f.raw)
}
