package facts

import (
	"cmp"
	"path"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// DirDataSources is the data source types the configuration files directly
// in one head directory declare, at the top level or inside a check block.
// Complete is false when a file there is a .tf.json, .tofu or .tofu.json
// file, is not a regular file or does not parse, or a data block does not
// have two labels: Types may then miss a type.
type DirDataSources struct {
	Dir      string   `json:"dir"`
	Types    []string `json:"types"`
	Complete bool     `json:"complete"`
}

// readTarget is one resource or data block whose changed attributes the
// scan looks for. A whole-object read is kept once for the block, not once
// per attribute, so the readers it records stay within maxReaders whatever
// the number of changed attributes.
type readTarget struct {
	attrs      []*ChangedAttribute
	whole      []Reader
	wholeCount int
}

// addReader records one read of a, at most maxReaders of them.
func addReader(a *ChangedAttribute, r Reader) {
	if a.Read != ReadUnknown {
		a.Read = ReadFound
	}
	if n := len(a.Readers); n > 0 && a.Readers[n-1] == r {
		return
	}
	if len(a.Readers) >= maxReaders {
		a.ReadersTruncated = true
		return
	}
	a.Readers = append(a.Readers, r)
}

// markReads sets the read state of every changed attribute of a resource or
// data block of a parsed, modified file: whether an expression of the
// module that holds the block, the files directly in its directory, reads
// it at head. Terraform resolves a block address only inside its module.
func markReads(files []ChangedFile, head *Tree, lim Limits) error {
	byDir := map[string]map[string]*readTarget{} // dir -> address -> target
	for fi := range files {
		f := &files[fi]
		for bi := range f.Blocks {
			b := &f.Blocks[bi]
			if b.Kind != "resource" && b.Kind != "data" {
				continue
			}
			for ai := range b.Attributes {
				if byDir[f.Dir] == nil {
					byDir[f.Dir] = map[string]*readTarget{}
				}
				t := byDir[f.Dir][b.Address]
				if t == nil {
					t = &readTarget{}
					byDir[f.Dir][b.Address] = t
				}
				a := &b.Attributes[ai]
				a.Read = ReadNone
				t.attrs = append(t.attrs, a)
			}
		}
	}
	for dir, targets := range byDir {
		if err := scanDirReads(head, dir, targets, lim); err != nil {
			return err
		}
		for _, t := range targets {
			for _, a := range t.attrs {
				finishReaders(a, t)
			}
		}
	}
	return nil
}

// scanDirReads parses every configuration file of dir and records each
// read of a target attribute. A file it cannot read, a .tf.json, .tofu,
// .tofu.json or override file among them, makes every target's state
// unknown, with that file as the reader.
func scanDirReads(head *Tree, dir string, targets map[string]*readTarget, lim Limits) error {
	type parsed struct {
		path string
		body *hclsyntax.Body
	}
	var bodies []parsed
	var unreadable []string
	for _, e := range head.DirEntries(dir) {
		if err := lim.expired(); err != nil {
			return err
		}
		if !configPath(e.Path) {
			continue
		}
		name := path.Base(e.Path)
		if !hclName(name) || overrideName(name) || e.Kind != KindFile {
			unreadable = append(unreadable, e.Path)
			continue
		}
		body, _, err := parseSyntax(name, e.Data, lim)
		if err != nil {
			unreadable = append(unreadable, e.Path)
			continue
		}
		bodies = append(bodies, parsed{e.Path, body})
	}
	if len(unreadable) > 0 {
		for _, t := range targets {
			for _, a := range t.attrs {
				a.Read = ReadUnknown
				for _, p := range unreadable {
					addReader(a, Reader{Path: p, How: HowUnknown})
				}
			}
		}
		return nil
	}
	counted := map[string]bool{}
	for _, p := range bodies {
		for _, b := range p.body.Blocks {
			if _, isTarget := targets[blockAddress(b)]; isTarget && refBlock(b) {
				_, c := b.Body.Attributes["count"]
				_, fe := b.Body.Attributes["for_each"]
				counted[blockAddress(b)] = c || fe
			}
		}
	}
	for _, p := range bodies {
		w := &readWalker{file: p.path, targets: targets, counted: counted, lim: lim}
		for _, b := range p.body.Blocks {
			w.top = ""
			if refBlock(b) {
				w.top = blockAddress(b)
			}
			_ = hclsyntax.Walk(b, w) // the walker reports no diagnostics; it keeps its own error
		}
		for _, a := range p.body.Attributes {
			w.top = ""
			_ = hclsyntax.Walk(a, w)
		}
		if w.err != nil {
			return w.err
		}
	}
	return nil
}

// refBlock reports a resource or data block with two labels, the blocks a
// reference can name.
func refBlock(b *hclsyntax.Block) bool {
	return (b.Type == "resource" || b.Type == "data") && len(b.Labels) == 2
}

// finishReaders adds the whole-object reads of its block to an attribute,
// sorts its readers, caps them at maxReaders and sets the read state.
func finishReaders(a *ChangedAttribute, t *readTarget) {
	if t.wholeCount > 0 && a.Read == ReadNone {
		a.Read = ReadFound
	}
	a.Readers = append(a.Readers, t.whole...)
	slices.SortFunc(a.Readers, func(x, y Reader) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Line, y.Line), cmp.Compare(x.How, y.How))
	})
	a.Readers = slices.Compact(a.Readers)
	if len(a.Readers) > maxReaders || t.wholeCount > len(t.whole) {
		a.ReadersTruncated = true
	}
	if len(a.Readers) > maxReaders {
		a.Readers = a.Readers[:maxReaders]
	}
}

// walkCheckEvery is how many nodes the walker visits between deadline checks.
const walkCheckEvery = 1024

// readWalker finds the references to target blocks in one file. top is the
// address of the resource or data block being walked, for self. err is the
// deadline error; once set, the walker does no more work.
type readWalker struct {
	file    string
	top     string
	targets map[string]*readTarget
	counted map[string]bool
	stack   []hclsyntax.Node
	lim     Limits
	nodes   int
	err     error
}

func (w *readWalker) Enter(n hclsyntax.Node) hcl.Diagnostics {
	w.stack = append(w.stack, n)
	if w.err != nil {
		return nil
	}
	if w.nodes++; w.nodes%walkCheckEvery == 0 {
		if w.err = w.lim.expired(); w.err != nil {
			return nil
		}
	}
	if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok {
		w.reference(st, w.stack[:len(w.stack)-1])
	}
	return nil
}

func (w *readWalker) Exit(hclsyntax.Node) hcl.Diagnostics {
	w.stack = w.stack[:len(w.stack)-1]
	return nil
}

// reference records what one reference reads of a target block; anc are
// its ancestors, innermost last.
func (w *readWalker) reference(st *hclsyntax.ScopeTraversalExpr, anc []hclsyntax.Node) {
	t := st.Traversal
	addr, steps, self := "", hcl.Traversal(nil), false
	attr := func(i int) string {
		if i < len(t) {
			if a, ok := t[i].(hcl.TraverseAttr); ok {
				return a.Name
			}
		}
		return ""
	}
	switch root := t.RootName(); root {
	case "self":
		addr, steps, self = w.top, t[1:], true
	case "data":
		if typ, name := attr(1), attr(2); typ != "" && name != "" {
			addr, steps = "data."+typ+"."+name, t[3:]
		}
	default:
		if name := attr(1); name != "" {
			addr, steps = root+"."+name, t[2:]
		}
	}
	tg, ok := w.targets[addr]
	if !ok || addr == "" {
		return
	}
	name, whole := readOf(steps, st, anc, w.counted[addr])
	r := Reader{Path: w.file, Line: st.SrcRange.Start.Line}
	if whole {
		r.How = HowObject
		tg.wholeCount++
		if n := len(tg.whole); len(tg.whole) < maxReaders && (n == 0 || tg.whole[n-1] != r) {
			tg.whole = append(tg.whole, r)
		}
		return
	}
	for _, a := range tg.attrs {
		switch {
		case name == a.Name && self:
			r.How = HowSelf
		case name == a.Name:
			r.How = HowName
		case a.Name == "tags" && name == "tags_all":
			r.How = HowTagsAll
		default:
			continue
		}
		addReader(a, r)
	}
}

// readOf returns the attribute a reference reads after a block address, or
// whole when it reads the whole object or an attribute it cannot name.
// steps are the reference's own steps after the address, node the
// reference and anc its ancestors, innermost last. With counted, the first
// index or splat selects instances; any other index names an attribute
// only by a literal string key.
func readOf(steps hcl.Traversal, node hclsyntax.Node, anc []hclsyntax.Node, counted bool) (name string, whole bool) {
	sel := counted
	if name, done, whole := stepRead(steps, &sel); done {
		return name, whole
	}
	cur := node
	for _, a := range slices.Backward(anc) {
		switch p := a.(type) {
		case *hclsyntax.IndexExpr:
			if p.Collection != cur {
				return "", true
			}
			if !sel {
				k, ok := literalString(p.Key)
				return k, !ok
			}
			sel = false
		case *hclsyntax.SplatExpr:
			if p.Source != cur || !sel {
				return "", true
			}
			sel = false
			each, ok := eachSteps(p.Each)
			if !ok {
				return "", true
			}
			if name, done, whole := stepRead(each, &sel); done {
				return name, whole
			}
		case *hclsyntax.RelativeTraversalExpr:
			if p.Source != cur {
				return "", true
			}
			if name, done, whole := stepRead(p.Traversal, &sel); done {
				return name, whole
			}
		default:
			return "", true
		}
		cur = a
	}
	return "", true
}

// stepRead reads traversal steps: the first attribute step names what is
// read; done is false when the steps end before one.
func stepRead(steps hcl.Traversal, sel *bool) (name string, done, whole bool) {
	for _, s := range steps {
		switch x := s.(type) {
		case hcl.TraverseAttr:
			return x.Name, true, false
		case hcl.TraverseIndex:
			if *sel {
				*sel = false
				continue
			}
			if x.Key.IsKnown() && !x.Key.IsNull() && x.Key.Type() == cty.String {
				return x.Key.AsString(), true, false
			}
			return "", true, true
		case hcl.TraverseSplat:
			if *sel {
				*sel = false
				continue
			}
			return "", true, true
		default:
			return "", true, true
		}
	}
	return "", false, false
}

// eachSteps flattens the per-element part of a splat into its steps.
func eachSteps(e hclsyntax.Expression) (hcl.Traversal, bool) {
	switch x := e.(type) {
	case *hclsyntax.AnonSymbolExpr:
		return nil, true
	case *hclsyntax.RelativeTraversalExpr:
		inner, ok := eachSteps(x.Source)
		return append(inner, x.Traversal...), ok
	}
	return nil, false
}

// dataSources lists, per head directory with a configuration file, the
// data source types its files declare; see DirDataSources.
func dataSources(head *Tree, lim Limits) ([]DirDataSources, error) {
	var dirs []string
	seen := map[string]bool{}
	for _, p := range head.Paths {
		if d := path.Dir(p); configPath(p) && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	out := make([]DirDataSources, 0, len(dirs))
	for _, d := range dirs {
		ds := DirDataSources{Dir: d, Types: []string{}, Complete: true}
		for _, e := range head.DirEntries(d) {
			if err := lim.expired(); err != nil {
				return nil, err
			}
			name := path.Base(e.Path)
			if !configPath(e.Path) {
				continue
			}
			if !hclName(name) || e.Kind != KindFile {
				ds.Complete = false
				continue
			}
			body, _, err := parseSyntax(name, e.Data, lim)
			if err != nil {
				ds.Complete = false
				continue
			}
			for _, b := range body.Blocks {
				blocks := []*hclsyntax.Block{b}
				if b.Type == "check" {
					blocks = b.Body.Blocks // a check block may declare scoped data sources
				}
				for _, x := range blocks {
					switch {
					case x.Type != "data":
					case len(x.Labels) == 2:
						ds.Types = append(ds.Types, x.Labels[0])
					default:
						ds.Complete = false
					}
				}
			}
		}
		slices.Sort(ds.Types)
		ds.Types = slices.Compact(ds.Types)
		out = append(out, ds)
	}
	return out, nil
}
