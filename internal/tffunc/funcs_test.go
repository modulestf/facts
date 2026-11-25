package tffunc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestNamesSortedAndUnique(t *testing.T) {
	t.Parallel()
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Error("names is not sorted and unique")
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	s := Set("file", "uuid")
	if len(s) != len(names)-2 || s["file"] || s["uuid"] || !s["abs"] {
		t.Errorf("Set(file, uuid) has %d names, file %v, uuid %v, abs %v", len(s), s["file"], s["uuid"], s["abs"])
	}
	s["abs"] = false
	if !IsBuiltin("abs") || !Set()["abs"] {
		t.Error("changing a returned set changed the table")
	}
	n := Names()
	n[0] = "x"
	if names[0] == "x" {
		t.Error("changing Names changed the table")
	}
}

func TestSetPanicsOnUnknownName(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("Set(nosuchfunction) did not panic")
		}
	}()
	Set("nosuchfunction")
}

// minList is the size from which a literal list of built-in names counts as
// a second table. A filter over the table may be longer: it is the argument
// list of a Set call, which the check leaves out.
const minList = 20

// builtinName returns the built-in function a string literal names.
func builtinName(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil && all[s]
}

// isSetCall reports a call of tffunc.Set, or of Set inside this package.
func isSetCall(c *ast.CallExpr) bool {
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		x, ok := f.X.(*ast.Ident)
		return ok && x.Name == "tffunc" && f.Sel.Name == "Set"
	case *ast.Ident:
		return f.Name == "Set"
	}
	return false
}

// isNameSet reports a composite literal type that holds a list or set of
// strings: a slice or array of string, or a map from string to bool or to
// struct{}. A map to any other value, such as a class or a row, is a table
// about the functions, not a list of them.
func isNameSet(t ast.Expr) bool {
	switch t := t.(type) {
	case *ast.ArrayType:
		id, ok := t.Elt.(*ast.Ident)
		return ok && id.Name == "string"
	case *ast.MapType:
		if k, ok := t.Key.(*ast.Ident); !ok || k.Name != "string" {
			return false
		}
		switch v := t.Value.(type) {
		case *ast.Ident:
			return v.Name == "bool"
		case *ast.StructType:
			return len(v.Fields.List) == 0
		}
	}
	return false
}

// secondLists returns the line and size of each literal list of minList or
// more built-in names in a Go file: a string slice or array, a set map, or
// the arguments of a call other than Set.
func secondLists(fset *token.FileSet, path string, src any) (map[int]int, error) {
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[int]int{}
	count := func(n ast.Node, elts []ast.Expr) {
		seen := map[string]bool{}
		for _, e := range elts {
			if kv, ok := e.(*ast.KeyValueExpr); ok {
				e = kv.Key
			}
			if s, ok := builtinName(e); ok {
				seen[s] = true
			}
		}
		if len(seen) >= minList {
			out[fset.Position(n.Pos()).Line] = len(seen)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CompositeLit:
			if isNameSet(n.Type) {
				count(n, n.Elts)
			}
		case *ast.CallExpr:
			if !isSetCall(n) {
				count(n, n.Args)
			}
		}
		return true
	})
	return out, nil
}

// No Go file but this package's table holds a literal list of the
// functions. A list of the functions some code allows or refuses is a filter
// over the table: tffunc.Set with the names it leaves out. Test files are
// left out: a test may name the functions a filter must refuse, as an
// independent check of that filter.
func TestNoSecondList(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	own := filepath.Join("..", "..", "internal", "tffunc", "funcs.go")
	err := filepath.WalkDir(filepath.Join("..", ".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == "testdata" || n == "vendor" || (len(n) > 1 && strings.HasPrefix(n, ".") && n != "..") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || filepath.Clean(path) == own {
			return nil
		}
		lists, err := secondLists(fset, path, nil)
		if err != nil {
			return err
		}
		for line, n := range lists {
			t.Errorf("%s:%d lists %d built-in functions: take the table or a filter over it from package tffunc", path, line, n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The check catches a copy in each form and passes a filter and a table
// about the functions.
func TestSecondListForms(t *testing.T) {
	t.Parallel()
	quoted := make([]string, minList)
	for i, n := range names[:minList] {
		quoted[i] = strconv.Quote(n)
	}
	list := strings.Join(quoted, ", ")
	pairs := func(v string) string {
		var b strings.Builder
		for _, q := range quoted {
			b.WriteString(q + ": " + v + ", ")
		}
		return b.String()
	}
	for _, c := range []struct {
		name, src string
		want      bool
	}{
		{"slice", "var x = []string{" + list + "}", true},
		{"array", "var x = [...]string{" + list + "}", true},
		{"bool map", "var x = map[string]bool{" + pairs("true") + "}", true},
		{"struct map", "var x = map[string]struct{}{" + pairs("{}") + "}", true},
		{"call", "var x = toSet(" + list + ")", true},
		{"short list", "var x = []string{" + strings.Join(quoted[1:], ", ") + "}", false},
		{"filter", "var x = tffunc.Set(" + list + ")", false},
		{"class table", "var x = map[string]int{" + pairs("1") + "}", false},
	} {
		got, err := secondLists(token.NewFileSet(), "p.go", "package p\n"+c.src+"\n")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (len(got) > 0) != c.want {
			t.Errorf("%s: found %v, want a list found %v", c.name, got, c.want)
		}
	}
}
