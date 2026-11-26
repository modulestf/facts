package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The two mutations. A guard is an if with no else, or a case of a switch
// with no tag, whose body ends in return, break, continue, goto or panic;
// its condition becomes false && (cond), so the guard never fires. An
// operand of an && or || chain becomes (true || (x)) or (false && (x)), so
// the chain reads as if the operand were not there. Both rewrites keep
// every identifier referenced, so a mutant never fails to build only
// because a variable or an import became unused.
const (
	kindGuard   = "guard never fires"
	kindOperand = "operand dropped"
)

// Mutant is one rewrite of one file.
type Mutant struct {
	File string // repository-relative, slash separated
	Line int
	Kind string
	Code string // the original text the rewrite wraps, on one line
	Src  []byte // the whole mutated file
}

type edit struct {
	start, end int // byte offsets of the wrapped node
	open, shut string
	line       int
	kind       string
}

// mutants returns the mutants of a Go file whose wrapped node has a line
// in changed. The order is by position in the file.
func mutants(name string, src []byte, changed map[int]bool) ([]Mutant, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// The file's own lines: a //line directive must not move them, since the
	// diff counts the lines of the file as it is on disk.
	pos := func(p token.Pos) token.Position { return fset.PositionFor(p, false) }
	touched := func(n ast.Node) bool {
		for l := pos(n.Pos()).Line; l <= pos(n.End()).Line; l++ {
			if changed[l] {
				return true
			}
		}
		return false
	}
	var edits []edit
	add := func(n ast.Node, open, shut, kind string) {
		if touched(n) {
			edits = append(edits, edit{
				start: pos(n.Pos()).Offset, end: pos(n.End()).Offset,
				open: open, shut: shut, line: pos(n.Pos()).Line, kind: kind,
			})
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.IfStmt:
			if x.Else == nil && terminates(x.Body.List) {
				add(x.Cond, "false && (", ")", kindGuard)
			}
		case *ast.SwitchStmt:
			if x.Tag != nil {
				break
			}
			for _, s := range x.Body.List {
				c, ok := s.(*ast.CaseClause)
				if !ok || len(c.List) != 1 || !terminates(c.Body) {
					continue
				}
				add(c.List[0], "false && (", ")", kindGuard)
			}
		case *ast.BinaryExpr:
			if x.Op != token.LAND && x.Op != token.LOR {
				break
			}
			open, shut := "(true || (", "))"
			if x.Op == token.LOR {
				open, shut = "(false && (", "))"
			}
			for _, o := range []ast.Expr{x.X, x.Y} {
				// An operand that is itself the same chain is split
				// further down; dropping it whole repeats its parts.
				if b, ok := o.(*ast.BinaryExpr); ok && b.Op == x.Op {
					continue
				}
				add(o, open, shut, kindOperand)
			}
		}
		return true
	})
	slices.SortStableFunc(edits, func(a, b edit) int { return a.start - b.start })
	out := make([]Mutant, 0, len(edits))
	for _, e := range edits {
		var b bytes.Buffer
		b.Grow(len(src) + len(e.open) + len(e.shut))
		b.Write(src[:e.start])
		b.WriteString(e.open)
		b.Write(src[e.start:e.end])
		b.WriteString(e.shut)
		b.Write(src[e.end:])
		out = append(out, Mutant{
			File: name, Line: e.line, Kind: e.kind,
			Code: strings.Join(strings.Fields(string(src[e.start:e.end])), " "),
			Src:  b.Bytes(),
		})
	}
	return out, nil
}

// terminates reports a statement list whose last statement leaves the
// enclosing block: return, break, continue, goto or a call of panic.
func terminates(list []ast.Stmt) bool {
	if len(list) == 0 {
		return false
	}
	switch s := list[len(list)-1].(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return true
	case *ast.ExprStmt:
		c, ok := s.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := c.Fun.(*ast.Ident)
		return ok && id.Name == "panic"
	}
	return false
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// changedLines reads a unified diff with no context lines (git diff -U0)
// and returns, per changed Go file, the line numbers it adds or changes in
// the new version. Test files, files under a testdata directory, deleted
// files and names git had to quote are left out.
func changedLines(r io.Reader) (map[string]map[int]bool, error) {
	files := map[string]map[int]bool{}
	var cur map[int]bool
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			cur = nil
			name, ok := strings.CutPrefix(line[4:], "b/")
			// git ends the name with a tab when it holds a space, so a
			// reader that splits on whitespace can tell it from a date.
			name = strings.TrimSuffix(name, "\t")
			if !ok || !wanted(name) {
				continue
			}
			cur = map[int]bool{}
			files[name] = cur
		case strings.HasPrefix(line, "@@"):
			if cur == nil {
				continue
			}
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("malformed hunk header %q", line)
			}
			start, err := strconv.Atoi(m[1])
			if err != nil {
				return nil, err
			}
			n := 1
			if m[2] != "" {
				if n, err = strconv.Atoi(m[2]); err != nil {
					return nil, err
				}
			}
			for l := start; l < start+n; l++ {
				cur[l] = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for name, lines := range files {
		if len(lines) == 0 {
			delete(files, name)
		}
	}
	return files, nil
}

func wanted(name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	return !slices.Contains(strings.Split(path.Dir(name), "/"), "testdata")
}
