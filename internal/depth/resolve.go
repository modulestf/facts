package depth

import (
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
	"github.com/zclconf/go-cty/cty"
)

// A pattern is the set of strings an expression can produce: a sequence of
// pieces, each a literal, the path.module or path.root directory, any
// string, or a value read from elsewhere in the tree.
type piece struct {
	kind int
	s    string
}

const (
	pLit  = iota
	pMod  // path.module, s holding the module directory
	pRoot // path.root or path.cwd
	pStar // any string
	pSrc  // a value read by reference: its own string expressions are scanned where they are written
)

type pattern []piece

// Bounds of the evaluation.
const (
	maxPatterns   = 16 // patterns of one expression; more is any string
	maxLocalDepth = 32 // references traced through one another
	maxScanned    = 500000
	maxSteps      = 2000000 // evaluation steps of one scan: tracing fans out through every source
)

var (
	anyString = []pattern{{{kind: pStar}}}
	fromSrc   = []pattern{{{kind: pSrc}}}
)

// loc is where a scanned expression is: a file and, for configuration, its
// item and argument.
type loc struct {
	file string
	it   *item
	a    *arg
}

// sinkRe matches the name of an argument whose string a provider or
// Terraform may read as a path: a file sink. README.md lists the same
// names.
var sinkRe = regexp.MustCompile(`^(path|paths|file|files|filename|dir|directory|source|source_dir|source_file|chart|config|config_path|config_paths|template|template_dir|program|script|scripts|content_dir|output_path)$|^[a-z0-9_]+_(path|paths|file|files|filename|dir|dirs|directory|template|script|scripts)$`)

// provisionerSinks are the provisioner arguments that read files: the file
// provisioner's source, remote-exec's script and scripts, local-exec's
// working_dir and interpreter. A local-exec command is searched instead.
var provisionerSinks = map[string]bool{"source": true, "script": true, "scripts": true, "working_dir": true, "interpreter": true}

// fileTypes are the resource and data types whose attributes are traced
// like files: their attribute is the argument of the same name.
var fileTypes = map[string]bool{"local_file": true, "local_sensitive_file": true, "archive_file": true,
	"template_file": true, "template_dir": true, "external": true}

// scanner resolves the strings of the head that reach a file sink: the file
// functions, any provisioner argument, and an argument sinkRe matches, at
// any nesting, in any block.
type scanner struct {
	head    *model
	links   *linker
	lim     facts.Limits
	changed map[string]bool    // changed paths, previous names included
	holds   map[string]bool    // directories holding a changed path
	roots   []string           // directories a relative path or path.root resolves against
	callers map[string][]*item // a directory's callers: the module calls with a local source that resolves to it
	words   []string           // lower-case names of the changed paths and their directories

	hits       []loc // a sink string resolves to a changed file or a directory holding one
	unresolved []loc // a sink string does not resolve
	// strict is set when the head calls a function that is not built-in,
	// a provider-defined one included: light rule 4.
	strict  bool
	scanned int
	steps   int // evaluation steps, bounded by maxSteps
	over    bool
}

func newScanner(head *model, links *linker, lim facts.Limits, changed []string) *scanner {
	s := &scanner{head: head, links: links, lim: lim, changed: map[string]bool{}, holds: map[string]bool{},
		callers: map[string][]*item{}}
	words := map[string]bool{}
	for _, p := range changed {
		s.changed[strings.ToLower(p)] = true
		words[strings.ToLower(path.Base(p))] = true
		for _, d := range ancestors(p) {
			s.holds[strings.ToLower(d)] = true
			if d != "." {
				words[strings.ToLower(path.Base(d))] = true
			}
		}
	}
	s.words = slices.Sorted(maps.Keys(words))
	for _, d := range head.configDirs() {
		for _, it := range head.mods[d].items {
			if it.kind != kModule {
				continue
			}
			if callee, local, ok := calleeOf(head, links, it); ok && local {
				s.callers[callee] = append(s.callers[callee], it)
			}
		}
	}
	s.roots = append([]string{"."}, head.configDirs()...)
	s.roots = slices.Compact(s.roots)
	return s
}

// run scans every argument of every Terraform-read head file. A module's
// source is left to the call graph and a variable's type is not a value.
func (s *scanner) run() {
	scan := func(it *item, c ctx, file string) {
		for _, a := range it.args {
			if it.kind == kModule && a.name == "source" || it.kind == kVariable && a.name == "type" {
				continue
			}
			for i, e := range a.exprs {
				l := loc{file, it, a}
				c := s.scoped(c, it, a.scopes[i])
				s.walk(e, c, l)
				switch {
				case it.kind == kTFVar:
					// a value: traced from the sink its variable lands in
				case a.name == "provisioner":
					if provisionerSinks[a.attrs[i]] {
						s.sink(e, c, l)
					} else if a.attrs[i] == "command" {
						s.command(e, l)
					}
				case it.kind == kModule:
					// a local callee's sinks trace the value; a remote one may read it as a path
					if _, local, ok := calleeOf(s.head, s.links, it); (!ok || !local) && sinkRe.MatchString(a.attrs[i]) {
						s.sink(e, c, l)
					}
				case sinkRe.MatchString(a.attrs[i]):
					s.sink(e, c, l)
				}
			}
		}
	}
	for _, d := range s.head.configDirs() {
		mod := s.head.mods[d]
		for _, it := range mod.items {
			scan(it, ctx{dir: d, mod: mod}, it.file)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(s.head.files)) {
		dir := path.Dir(p)
		for _, it := range s.head.files[p].items {
			scan(it, ctx{dir: dir, mod: s.head.mods[dir]}, p)
		}
	}
}

// ctx is the module an expression is evaluated in.
type ctx struct {
	dir   string
	mod   *module
	it    *item              // the block the expression is in, for each.key and each.value
	iter  map[string]binding // iteration variables: a for expression's, a dynamic block's
	depth int
}

// binding is an iteration variable: an element of the collection coll,
// evaluated in its own context, or, for a key, keys.
type binding struct {
	coll hclsyntax.Expression
	in   ctx
	key  bool
	dyn  bool // a dynamic block's iterator, read as <name>.value and <name>.key
}

// with returns c with one more iteration variable bound.
func (c ctx) with(name string, b binding) ctx {
	iter := maps.Clone(c.iter)
	if iter == nil {
		iter = map[string]binding{}
	}
	iter[name] = b
	c.iter = iter
	return c
}

// scoped returns c for an expression of item it inside the dynamic
// blocks scope: each iterator is bound to its block's for_each collection.
func (s *scanner) scoped(c ctx, it *item, scope []dynScope) ctx {
	c.it = it
	for _, d := range scope {
		coll := d.forEach
		if coll == nil {
			coll = &hclsyntax.AnonSymbolExpr{} // any string
		}
		c = c.with(d.name, binding{coll: coll, in: c, dyn: true})
	}
	return c
}

// walk visits every function call in e: a function that is not built-in
// sets strict, and a file function's path is a sink.
func (s *scanner) walk(e hclsyntax.Expression, c ctx, l loc) {
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if s.scanned++; s.scanned > maxScanned {
			s.over = true
			return hcl.Diagnostics{{}} // stops the walk
		}
		if x, ok := n.(*hclsyntax.FunctionCallExpr); ok {
			s.call(x, c, l)
		}
		return nil
	})
}

// sink resolves a value a file sink reads: every string it can be must be
// a literal, or path.module or path.root joined with literals. A list,
// set, map or object has each element resolved.
func (s *scanner) sink(e hclsyntax.Expression, c ctx, l loc) {
	switch x := e.(type) {
	case *hclsyntax.TupleConsExpr:
		for _, el := range x.Exprs {
			s.sink(el, c, l)
		}
		return
	case *hclsyntax.ObjectConsExpr:
		for _, kv := range x.Items {
			s.sink(kv.ValueExpr, c, l)
		}
		return
	case *hclsyntax.ParenthesesExpr:
		s.sink(x.Expression, c, l)
		return
	case *hclsyntax.ConditionalExpr:
		s.sink(x.TrueResult, c, l)
		s.sink(x.FalseResult, c, l)
		return
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() != cty.String {
			return // a number, a bool or null names no file
		}
	case *hclsyntax.BinaryOpExpr, *hclsyntax.UnaryOpExpr:
		return // a number or a bool
	case *hclsyntax.FunctionCallExpr:
		if name, ok := funcName(x.Name); ok && funcClass[name] == fnData {
			return // an encoding, hash, number, boolean or time
		}
	}
	ps := s.eval(e, c)
	if len(ps) == 0 {
		s.unresolved = append(s.unresolved, l) // a value that is not a plain string or collection of them
		return
	}
	for _, p := range ps {
		s.resolveTo(p, l)
	}
}

// command searches the literal text of a local-exec command for the name
// of a changed path or of a directory holding one: the shell may read it.
func (s *scanner) command(e hclsyntax.Expression, l loc) {
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		lit, ok := n.(*hclsyntax.LiteralValueExpr)
		if !ok || lit.Val.Type() != cty.String || lit.Val.IsNull() || !lit.Val.IsKnown() {
			return nil
		}
		text := strings.ToLower(lit.Val.AsString())
		for _, w := range s.words {
			if strings.Contains(text, w) {
				s.unresolved = append(s.unresolved, l)
				break
			}
		}
		return nil
	})
}

// call checks one function call: a function that is not built-in blocks
// light; a file function's path must resolve, and a template a
// templatefile call reads is scanned too.
func (s *scanner) call(x *hclsyntax.FunctionCallExpr, c ctx, l loc) {
	name, builtin := funcName(x.Name)
	if !builtin {
		s.strict = true
		return
	}
	cls := funcClass[name]
	if (cls != fnRead && cls != fnFileset) || len(x.Args) == 0 {
		return
	}
	ps := s.eval(x.Args[0], c)
	if len(ps) == 0 {
		s.unresolved = append(s.unresolved, l)
	}
	for _, p := range ps {
		if !p.exact() {
			s.unresolved = append(s.unresolved, l)
			continue
		}
		s.resolveTo(p, l)
		for _, f := range s.places(p) {
			if name == "templatefile" && !f.outside && !f.bad {
				s.template(f.real, c, l)
			}
		}
	}
}

// template scans the function calls of a template file a templatefile
// call reads, in the calling module.
func (s *scanner) template(p string, c ctx, l loc) {
	e := s.head.tree.Entries[p]
	switch {
	case e == nil:
		return // Terraform fails on a missing template; validate reports it
	case e.Kind != facts.KindFile:
		s.unresolved = append(s.unresolved, l)
		return
	}
	w := &jsonWalker{file: p, lim: s.lim}
	if !w.bounded(string(e.Data)) {
		s.unresolved = append(s.unresolved, l)
		return
	}
	t, diags := hclsyntax.ParseTemplate(e.Data, p, hcl.InitialPos)
	if diags.HasErrors() {
		s.unresolved = append(s.unresolved, l)
		return
	}
	strict := s.strict
	s.walk(t, c, l)
	s.strict = strict // a template is not a Terraform-read file
}

// eval returns the patterns of the strings e can produce; none when e is
// not a string.
func (s *scanner) eval(e hclsyntax.Expression, c ctx) []pattern {
	if s.spent() {
		return anyString
	}
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String && x.Val.IsKnown() && !x.Val.IsNull() {
			return []pattern{{{kind: pLit, s: x.Val.AsString()}}}
		}
		return fromSrc // a number, a bool or null: no path
	case *hclsyntax.TemplateExpr:
		out := []pattern{{}}
		for _, p := range x.Parts {
			ps := s.eval(p, c)
			if len(ps) == 0 {
				ps = anyString
			}
			out = product(out, ps)
		}
		return out
	case *hclsyntax.TemplateWrapExpr:
		return s.eval(x.Wrapped, c)
	case *hclsyntax.ParenthesesExpr:
		return s.eval(x.Expression, c)
	case *hclsyntax.ScopeTraversalExpr:
		return s.ref(x.Traversal, c)
	case *hclsyntax.ConditionalExpr:
		return union(s.eval(x.TrueResult, c), s.eval(x.FalseResult, c))
	case *hclsyntax.FunctionCallExpr:
		return s.evalCall(x, c)
	case *hclsyntax.IndexExpr:
		return s.eval(x.Collection, c) // one of its elements
	case *hclsyntax.RelativeTraversalExpr:
		return s.valueAt(x.Source, attrNames(x.Traversal), c)
	case *hclsyntax.SplatExpr:
		if rt, ok := x.Each.(*hclsyntax.RelativeTraversalExpr); ok {
			if _, anon := rt.Source.(*hclsyntax.AnonSymbolExpr); anon {
				return s.elementsAt(x.Source, attrNames(rt.Traversal), c)
			}
		}
		return anyString
	case *hclsyntax.AnonSymbolExpr:
		return anyString
	case *hclsyntax.TupleConsExpr:
		out := fromSrc
		for _, el := range x.Exprs {
			out = union(out, s.eval(el, c))
		}
		return out
	case *hclsyntax.ObjectConsExpr:
		out := fromSrc
		for _, kv := range x.Items {
			out = union(out, s.eval(kv.ValueExpr, c))
		}
		return out
	case *hclsyntax.BinaryOpExpr, *hclsyntax.UnaryOpExpr:
		return fromSrc // a number or a bool
	case *hclsyntax.ForExpr:
		return s.eval(x.ValExpr, s.forScope(x, c))
	}
	return anyString
}

// forScope binds a for expression's variables.
func (s *scanner) forScope(x *hclsyntax.ForExpr, c ctx) ctx {
	in := c.with(x.ValVar, binding{coll: x.CollExpr, in: c})
	if x.KeyVar != "" {
		in = in.with(x.KeyVar, binding{coll: x.CollExpr, in: c, key: true})
	}
	return in
}

// element returns the strings an iteration variable, or the attributes
// attrs of it, can be. A dynamic block's iterator and each are read
// through .value and .key.
func (s *scanner) element(b binding, attrs []string) []pattern {
	if s.spent() || b.in.depth >= maxLocalDepth {
		return anyString
	}
	b.in.depth++
	if b.dyn {
		if len(attrs) == 0 {
			return anyString
		}
		switch attrs[0] {
		case "value":
			attrs = attrs[1:]
		case "key":
			b.key = true
		default:
			return anyString
		}
	}
	if b.key {
		return keysOf(s.eval(b.coll, b.in))
	}
	return s.elementsAt(b.coll, attrs, b.in)
}

// elementsAt returns the strings the elements of collection e can be, at
// the attribute path.
func (s *scanner) elementsAt(e hclsyntax.Expression, path []string, c ctx) []pattern {
	if s.spent() {
		return anyString
	}
	if len(path) == 0 {
		return s.eval(e, c)
	}
	var out []pattern
	switch x := e.(type) {
	case *hclsyntax.TupleConsExpr:
		for _, el := range x.Exprs {
			out = union(out, s.valueAt(el, path, c))
		}
		return orPass(out)
	case *hclsyntax.ObjectConsExpr:
		for _, kv := range x.Items {
			out = union(out, s.valueAt(kv.ValueExpr, path, c))
		}
		return orPass(out)
	case *hclsyntax.ForExpr:
		return s.valueAt(x.ValExpr, path, s.forScope(x, c))
	case *hclsyntax.IndexExpr:
		return s.elementsAt(x.Collection, path, c) // an element's elements: valueAt opens nested collections
	case *hclsyntax.ParenthesesExpr:
		return s.elementsAt(x.Expression, path, c)
	case *hclsyntax.ConditionalExpr:
		return union(s.elementsAt(x.TrueResult, path, c), s.elementsAt(x.FalseResult, path, c))
	case *hclsyntax.FunctionCallExpr:
		if name, ok := funcName(x.Name); ok && (passOn[name] || name == "try" || name == "coalesce") && !x.ExpandFinal {
			for _, a := range x.Args {
				out = union(out, s.elementsAt(a, path, c))
			}
			return orPass(out)
		}
	case *hclsyntax.ScopeTraversalExpr:
		return s.refAt(x.Traversal, path, c, true)
	}
	return s.eval(e, c)
}

// valueAt returns the strings value v can be at the attribute path.
func (s *scanner) valueAt(v hclsyntax.Expression, path []string, c ctx) []pattern {
	if s.spent() {
		return anyString
	}
	if len(path) == 0 {
		return s.eval(v, c)
	}
	key, rest := path[0], path[1:]
	switch x := v.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, kv := range x.Items {
			if keyName(kv.KeyExpr) == "" {
				return anyString // a computed key may be path[0]
			}
		}
		for _, kv := range x.Items {
			if keyName(kv.KeyExpr) == key {
				return s.valueAt(kv.ValueExpr, rest, c)
			}
		}
		return nil // the object has no such attribute
	case *hclsyntax.ForExpr, *hclsyntax.TupleConsExpr:
		return s.elementsAt(x, path, c) // a collection: an index, or flatten, opens it
	case *hclsyntax.IndexExpr:
		return s.elementsAt(x.Collection, path, c)
	case *hclsyntax.RelativeTraversalExpr:
		return s.valueAt(x.Source, append(attrNames(x.Traversal), path...), c)
	case *hclsyntax.ParenthesesExpr:
		return s.valueAt(x.Expression, path, c)
	case *hclsyntax.ConditionalExpr:
		return union(s.valueAt(x.TrueResult, path, c), s.valueAt(x.FalseResult, path, c))
	case *hclsyntax.FunctionCallExpr:
		if name, ok := funcName(x.Name); ok && (name == "merge" || name == "try" || name == "coalesce") && !x.ExpandFinal {
			var out []pattern
			for _, a := range x.Args {
				out = union(out, s.valueAt(a, path, c))
			}
			return orPass(out)
		}
	case *hclsyntax.ScopeTraversalExpr:
		return s.refAt(x.Traversal, path, c, false)
	}
	return s.eval(v, c)
}

// refAt returns the strings a reference can be at the attribute path, or
// its elements' when elems is set.
func (s *scanner) refAt(t hcl.Traversal, path []string, c ctx, elems bool) []pattern {
	if s.spent() || c.depth >= maxLocalDepth {
		return anyString
	}
	c.depth++
	root, sub := t.RootName(), attrNames(t[1:])
	get := func(e hclsyntax.Expression, in ctx, sub []string) []pattern {
		switch {
		case elems && len(sub) == 0:
			return s.elementsAt(e, path, in)
		case elems:
			return s.valueAt(e, sub, in) // elements of an element: by position, not by name
		}
		return s.valueAt(e, append(slices.Clone(sub), path...), in)
	}
	if b, ok := c.iter[root]; ok {
		if elems {
			return s.element(b, sub)
		}
		return s.element(b, append(sub, path...))
	}
	switch root {
	case "var":
		if c.mod == nil || len(sub) == 0 {
			return anyString
		}
		return s.traceVar(sub[0], c, func(src source) []pattern { return get(src.e, src.in, sub[1:]) })
	case "local":
		if c.mod == nil || len(sub) == 0 {
			return anyString
		}
		it := c.mod.byAddr["local."+sub[0]]
		if it == nil || len(it.args) != 1 || len(it.args[0].exprs) != 1 {
			return anyString
		}
		return get(it.args[0].exprs[0], c, sub[1:])
	case "each":
		if c.it == nil || elems {
			return anyString
		}
		fe := c.it.arg("for_each")
		if fe == nil || len(fe.exprs) != 1 {
			return anyString
		}
		return s.element(binding{coll: fe.exprs[0], in: c, dyn: true}, append(sub, path...))
	}
	return s.ref(t, c)
}

// attrNames returns the attribute names of a traversal, past any index or
// splat steps.
func attrNames(t hcl.Traversal) []string {
	var out []string
	for _, step := range t {
		if a, ok := step.(hcl.TraverseAttr); ok {
			out = append(out, a.Name)
		}
	}
	return out
}

// passOn are the collection functions that only pass their arguments'
// elements on.
var passOn = map[string]bool{"merge": true, "flatten": true, "concat": true, "tolist": true, "toset": true,
	"tomap": true, "values": true, "compact": true, "distinct": true, "sort": true, "reverse": true, "one": true,
	"setunion": true, "coalescelist": true, "element": true, "slice": true}

// orPass returns fromSrc for no strings: nothing a path can be read from.
func orPass(ps []pattern) []pattern {
	if len(ps) == 0 {
		return fromSrc
	}
	return ps
}

// keysOf returns the strings the keys of a collection can be: no path when
// the collection is external, else any string.
func keysOf(coll []pattern) []pattern {
	for _, p := range coll {
		if !p.onlySrc() {
			return anyString
		}
	}
	return fromSrc
}

// ref traces a reference to the strings it can be: path.module and
// path.root are directories; a local is its expression; a variable is its
// default and every in-repo assignment (callers' arguments, variables files
// of its directory); a module output is its value in a local callee; an
// attribute of a file type is its argument of the same name. Any other
// resource or data attribute is no path. Anything else is any string.
func (s *scanner) ref(t hcl.Traversal, c ctx) []pattern {
	attr := func(i int) string {
		if i < len(t) {
			if a, ok := t[i].(hcl.TraverseAttr); ok {
				return a.Name
			}
		}
		return ""
	}
	if c.depth >= maxLocalDepth {
		return anyString
	}
	c.depth++
	one := func(it *item, name string, in ctx) []pattern {
		if it == nil {
			return anyString
		}
		a := it.arg(name)
		if a == nil || len(a.exprs) != 1 {
			return anyString
		}
		return s.eval(a.exprs[0], in)
	}
	root := t.RootName()
	if _, ok := c.iter[root]; ok || root == "each" || (root == "var" || root == "local") && len(attrNames(t)) > 1 {
		return s.refAt(t, nil, c, false)
	}
	switch root {
	case "path":
		switch attr(1) {
		case "module":
			return []pattern{{{kind: pMod, s: c.dir}}} // the directory it is evaluated in
		case "root", "cwd":
			return []pattern{{{kind: pRoot}}}
		}
		return anyString
	case "local":
		if c.mod == nil {
			return anyString
		}
		it := c.mod.byAddr["local."+attr(1)]
		if it == nil || len(it.args) != 1 || len(it.args[0].exprs) != 1 {
			return anyString
		}
		return s.eval(it.args[0].exprs[0], c)
	case "var":
		if len(t) != 2 || c.mod == nil {
			return anyString
		}
		return s.traceVar(attr(1), c, func(src source) []pattern { return s.eval(src.e, src.in) })
	case "module":
		if c.mod == nil || len(t) != 3 {
			return anyString
		}
		call := c.mod.byAddr["module."+attr(1)]
		if call == nil {
			return anyString
		}
		callee, local, ok := calleeOf(s.head, s.links, call)
		if !ok || !local {
			return anyString
		}
		mod := s.head.mods[callee]
		return one(mod.byAddr["output."+attr(2)], "value", ctx{dir: callee, mod: mod, depth: c.depth})
	case "count", "self", "terraform":
		return anyString
	case "data":
		if c.mod == nil || c.mod.byAddr["data."+attr(1)+"."+attr(2)] == nil {
			return anyString
		}
		if !fileTypes[attr(1)] {
			return fromSrc
		}
		return one(c.mod.byAddr["data."+attr(1)+"."+attr(2)], after(t, 3), c)
	default:
		if c.mod == nil || c.mod.byAddr[root+"."+attr(1)] == nil {
			return anyString // not a block of this module
		}
		if !fileTypes[root] {
			return fromSrc
		}
		return one(c.mod.byAddr[root+"."+attr(1)], after(t, 2), c)
	}
}

// source is one in-repo value of a variable and where it is written.
type source struct {
	e  hclsyntax.Expression
	in ctx
}

// varSources returns the in-repo values of variable name of the context's
// module: its default and every assignment, wrappers left out. ok is false
// for a variable the module does not declare.
func (s *scanner) varSources(name string, c ctx) (out []source, ok bool) {
	v := c.mod.byAddr["var."+name]
	if v == nil {
		return nil, false
	}
	one := func(a *arg, in ctx) source {
		if len(a.exprs) != 1 {
			return source{nil, in} // a JSON value of several strings: does not resolve
		}
		return source{a.exprs[0], in}
	}
	if a := v.arg("default"); a != nil {
		out = append(out, one(a, c))
	}
	for _, call := range s.callers[c.dir] {
		if wrappersPath(call.file) {
			continue // a generated wrapper passes its own external input through
		}
		if a := call.arg(name); a != nil {
			out = append(out, one(a, ctx{dir: call.dir, mod: s.head.mods[call.dir], it: call, depth: c.depth}))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(s.head.files)) {
		fm := s.head.files[p]
		if fm.form != formVars || path.Dir(p) != c.dir {
			continue
		}
		for _, it := range fm.items {
			if it.addr == "var."+name {
				out = append(out, one(it.args[0], c))
			}
		}
	}
	return out, true
}

// traceVar returns the strings variable name can be, through each source
// with get. A variable with no in-repo source is an external input, which
// passes.
func (s *scanner) traceVar(name string, c ctx, get func(source) []pattern) []pattern {
	srcs, ok := s.varSources(name, c)
	if !ok {
		return anyString
	}
	if len(srcs) == 0 {
		return fromSrc // an external input: a caller outside the repository supplies it
	}
	var out []pattern
	for _, src := range srcs {
		if src.e == nil {
			return anyString
		}
		out = union(out, get(src))
	}
	return orPass(out)
}

// evalCall evaluates a function call: a function in a path does not
// resolve, except that try and coalesce trace to their arguments, lookup to its map and its
// default, and a collection function that passes elements on (merge,
// flatten, concat and the like) to its arguments.
func (s *scanner) evalCall(x *hclsyntax.FunctionCallExpr, c ctx) []pattern {
	name, builtin := funcName(x.Name)
	if !builtin || x.ExpandFinal {
		return anyString
	}
	args := x.Args
	switch {
	case funcClass[name] == fnData:
		return fromSrc // an encoding, hash, number, boolean or time: not a path
	case (name == "chomp" || name == "trimspace") && len(args) == 1:
		var out []pattern
		for _, p := range s.eval(args[0], c) {
			out = append(out, p.trimmed(name == "trimspace"))
		}
		return out
	case name == "try" || name == "coalesce" || passOn[name]:
	case name == "lookup":
		if len(args) < 2 {
			return anyString
		}
		if key, ok := litString(args[1]); ok {
			out := s.valueAt(args[0], []string{key}, c) // the map's value at a literal key
			for _, d := range args[2:] {
				out = union(out, s.eval(d, c))
			}
			return orPass(out)
		}
		args = append([]hclsyntax.Expression{args[0]}, args[2:]...)
	default:
		return anyString
	}
	var out []pattern
	for _, a := range args {
		out = union(out, s.eval(a, c))
	}
	if len(out) == 0 {
		return anyString
	}
	return out
}

// spent counts one evaluation step and reports whether the budget of
// maxSteps or the deadline is spent; the walk is then incomplete.
func (s *scanner) spent() bool {
	s.steps++
	if s.steps > maxSteps || s.steps%4096 == 0 && timeUp(s.lim) {
		s.over = true
	}
	return s.over
}

// trimmed applies chomp, or trimspace when space is set, to a pattern:
// the trailing newlines, or the leading and trailing white space, of its
// literal ends. A wildcard end keeps its pattern unchanged.
func (p pattern) trimmed(space bool) pattern {
	p = append(pattern{}, p...)
	if n := len(p); n > 0 && p[n-1].kind == pLit {
		if space {
			p[n-1].s = strings.TrimRight(p[n-1].s, " \t\n\r\v\f")
		} else {
			p[n-1].s = strings.TrimRight(p[n-1].s, "\r\n")
		}
	}
	if space && len(p) > 0 && p[0].kind == pLit {
		p[0].s = strings.TrimLeft(p[0].s, " \t\n\r\v\f")
	}
	return p.norm()
}

// norm merges adjacent literals and wildcards and drops empty literals.
func (p pattern) norm() pattern {
	var out pattern
	for _, x := range p {
		if x.kind == pLit && x.s == "" {
			continue
		}
		if n := len(out); n > 0 {
			if out[n-1].kind == pLit && x.kind == pLit {
				out[n-1].s += x.s
				continue
			}
			if out[n-1].kind == pStar && x.kind == pStar {
				continue
			}
		}
		out = append(out, x)
	}
	return out
}

// exact reports a pattern of literals with at most a leading directory.
func (p pattern) exact() bool {
	for i, x := range p {
		if x.kind == pStar || x.kind == pSrc || (x.kind == pMod || x.kind == pRoot) && i > 0 {
			return false
		}
	}
	return true
}

// onlySrc reports a pattern that is one value read by reference.
func (p pattern) onlySrc() bool { return len(p) == 1 && p[0].kind == pSrc }

// product concatenates every pattern of a with every one of b.
func product(a, b []pattern) []pattern {
	if len(a)*len(b) > maxPatterns {
		return anyString
	}
	var out []pattern
	for _, x := range a {
		for _, y := range b {
			out = append(out, append(append(pattern{}, x...), y...).norm())
		}
	}
	return out
}

func union(a, b []pattern) []pattern {
	if len(a)+len(b) > maxPatterns {
		return anyString
	}
	return append(append([]pattern{}, a...), b...)
}

// place is one file or directory a pattern resolves to.
type place struct {
	real    string
	outside bool // outside the tree: never a changed path
	bad     bool // a link that cannot be resolved
}

// places resolves an exact pattern against the module directory, or each
// root for path.root and a relative path.
func (s *scanner) places(p pattern) []place {
	bases := s.roots
	var sb strings.Builder
	for _, x := range p {
		switch x.kind {
		case pMod:
			bases = []string{x.s}
		case pLit:
			sb.WriteString(x.s)
		}
	}
	lit := sb.String()
	if len(p) > 0 && p[0].kind == pLit && (strings.HasPrefix(lit, "/") || strings.HasPrefix(lit, "~")) {
		return []place{{outside: true}}
	}
	var out []place
	for _, b := range bases {
		dst, outs, ok := s.links.join(b, lit)
		switch {
		case !ok:
			out = append(out, place{bad: true})
		case outs:
			out = append(out, place{outside: true})
		default:
			out = append(out, place{real: dst})
		}
		if cand := path.Clean(path.Join(b, lit)); !outside(cand) && cand != dst {
			out = append(out, place{real: cand}) // the lexical reading too: either may be a changed path
		}
	}
	return out
}

// resolveTo records whether one pattern of a sink value is a dependency
// on a changed path, or does not resolve.
func (s *scanner) resolveTo(p pattern, l loc) {
	if p.onlySrc() {
		return // a resource or data attribute: no path
	}
	if !p.exact() {
		s.unresolved = append(s.unresolved, l)
		return
	}
	if len(p) == 0 || len(p) == 1 && p[0].kind == pLit && p[0].s == "" {
		return // the empty string names no file
	}
	for _, f := range s.places(p) {
		switch {
		case f.bad:
			s.unresolved = append(s.unresolved, l)
		case !f.outside && (s.changed[strings.ToLower(f.real)] || s.holds[strings.ToLower(f.real)]):
			s.hits = append(s.hits, l)
		}
	}
}
