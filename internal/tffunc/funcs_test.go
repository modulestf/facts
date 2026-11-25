package tffunc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
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

// minList is the number of distinct built-in names from which a Go file
// holds a second table, a quarter of the table. Code that dispatches on
// names stays below it: the most a file names today is 22, in the depth
// package's resolver. A filter over the table may name more: it is the
// argument list of a Set call, which the check leaves out.
const minList = 30

// fieldSep splits a string literal into the words of a list such as
// strings.Fields would read.
var fieldSep = regexp.MustCompile(`[\s,]+`)

var ident = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// literalNames adds the built-in names a string literal holds: the whole
// value when it is one name, or each word when the value is only words, as
// a list split at run time would be.
func literalNames(lit *ast.BasicLit, seen map[string]bool) {
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if all[s] {
		seen[s] = true
		return
	}
	words := fieldSep.Split(strings.TrimSpace(s), -1)
	if len(words) < 2 {
		return
	}
	for _, w := range words {
		if !ident.MatchString(w) {
			return
		}
	}
	for _, w := range words {
		if all[w] {
			seen[w] = true
		}
	}
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

// isAboutTable reports a map literal type whose values are not a set
// marker: a map from a function to its class or its row is a table about
// the functions, so its keys are not a list of them. A named type is not
// resolved and counts as a list.
func isAboutTable(t ast.Expr) bool {
	m, ok := t.(*ast.MapType)
	if !ok {
		return false
	}
	switch v := m.Value.(type) {
	case *ast.Ident:
		return v.Name != "bool"
	case *ast.StructType:
		return len(v.Fields.List) != 0
	}
	return true
}

// fileNames returns the distinct built-in names the string literals of a Go
// file hold, wherever they are: one list, a list split in parts, a string
// split at run time, switch cases or a named set type. The arguments of a
// Set call and the keys of a table about the functions are left out.
func fileNames(fset *token.FileSet, path string, src any) (map[string]bool, error) {
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if isSetCall(n) {
				return false
			}
		case *ast.CompositeLit:
			if isAboutTable(n.Type) {
				for _, e := range n.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						e = kv.Value
					}
					ast.Inspect(e, visit)
				}
				return false
			}
		case *ast.BasicLit:
			if n.Kind == token.STRING {
				literalNames(n, seen)
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	return seen, nil
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
		seen, err := fileNames(fset, path, nil)
		if err != nil {
			return err
		}
		if len(seen) >= minList {
			t.Errorf("%s names %d built-in functions: take the table or a filter over it from package tffunc", path, len(seen))
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
	half := minList / 2
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
		{"split list", "var x = []string{" + strings.Join(quoted[:half], ", ") + "}\nvar y = []string{" + strings.Join(quoted[half:], ", ") + "}", true},
		{"fields string", "var x = strings.Fields(" + strconv.Quote(strings.Join(names[:minList], " ")) + ")", true},
		{"comma string", "var x = strings.Split(" + strconv.Quote(strings.Join(names[:minList], ",")) + `, ",")`, true},
		{"switch", "func f(s string) bool {\n\tswitch s {\n\tcase " + list + ":\n\t\treturn true\n\t}\n\treturn false\n}", true},
		{"named set", "type nameSet map[string]bool\nvar x = nameSet{" + pairs("true") + "}", true},
		{"short list", "var x = []string{" + strings.Join(quoted[1:], ", ") + "}", false},
		{"prose", `var x = "the file is not one of the files a fixture may read: " + "split it"`, false},
		{"filter", "var x = tffunc.Set(" + list + ")", false},
		{"class table", "var x = map[string]int{" + pairs("1") + "}", false},
	} {
		got, err := fileNames(token.NewFileSet(), "p.go", "package p\n"+c.src+"\n")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if (len(got) >= minList) != c.want {
			t.Errorf("%s: %d names, want a list found %v", c.name, len(got), c.want)
		}
	}
}
