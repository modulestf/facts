package facts

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// DirDataSources is the data source types the configuration files directly
// in one head directory declare. Complete is false when a file there is a
// .tf.json file, is not a regular file or does not parse, or a data block
// does not have two labels: Types may then miss a type.
type DirDataSources struct {
	Dir      string   `json:"dir"`
	Types    []string `json:"types"`
	Complete bool     `json:"complete"`
}

// markReads sets the read state of every changed attribute of a resource or
// data block of a parsed, modified file: whether an expression of the
// module that holds the block, the files directly in its directory, reads
// it at head. Terraform resolves a block address only inside its module.
func markReads(files []ChangedFile, head *Tree, lim Limits) error {
	byDir := map[string]map[string][]*ChangedAttribute{} // dir -> address -> attributes
	for fi := range files {
		f := &files[fi]
		for bi := range f.Blocks {
			b := &f.Blocks[bi]
			if b.Kind != "resource" && b.Kind != "data" {
				continue
			}
			for ai := range b.Attributes {
				if byDir[f.Dir] == nil {
					byDir[f.Dir] = map[string][]*ChangedAttribute{}
				}
				byDir[f.Dir][b.Address] = append(byDir[f.Dir][b.Address], &b.Attributes[ai])
			}
		}
	}
	for dir, targets := range byDir {
		if err := scanDirReads(head, dir, targets, lim); err != nil {
			return err
		}
		for _, as := range targets {
			for _, a := range as {
				finishReaders(a)
			}
		}
	}
	return nil
}

// scanDirReads parses every configuration file of dir and records each
// read of a target attribute. A file it cannot read makes every target's
// state unknown, with that file as the reader.
func scanDirReads(head *Tree, dir string, targets map[string][]*ChangedAttribute, lim Limits) error {
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
		if strings.HasSuffix(name, ".tf.json") || overrideName(name) || e.Kind != KindFile {
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
	for _, as := range targets {
		for _, a := range as {
			a.Read = ReadNone
			for _, p := range unreadable {
				a.Read = ReadUnknown
				a.Readers = append(a.Readers, Reader{Path: p, How: HowUnknown})
			}
		}
	}
	if len(unreadable) > 0 {
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
		if err := lim.expired(); err != nil {
			return err
		}
		w := &readWalker{file: p.path, targets: targets, counted: counted}
		for _, b := range p.body.Blocks {
			w.top = ""
			if refBlock(b) {
				w.top = blockAddress(b)
			}
			_ = hclsyntax.Walk(b, w) // the walker reports no diagnostics
		}
		for _, a := range p.body.Attributes {
			w.top = ""
			_ = hclsyntax.Walk(a, w)
		}
	}
	return nil
}

// refBlock reports a resource or data block with two labels, the blocks a
// reference can name.
func refBlock(b *hclsyntax.Block) bool {
	return (b.Type == "resource" || b.Type == "data") && len(b.Labels) == 2
}

// finishReaders sorts and deduplicates the readers of an attribute, caps
// them at maxReaders and sets the read state.
func finishReaders(a *ChangedAttribute) {
	slices.SortFunc(a.Readers, func(x, y Reader) int {
		return cmp.Or(cmp.Compare(x.Path, y.Path), cmp.Compare(x.Line, y.Line), cmp.Compare(x.How, y.How))
	})
	a.Readers = slices.Compact(a.Readers)
	if a.Read == ReadNone && len(a.Readers) > 0 {
		a.Read = ReadFound
	}
	if len(a.Readers) > maxReaders {
		a.Readers, a.ReadersTruncated = a.Readers[:maxReaders], true
	}
}

// readWalker finds the references to target blocks in one file. top is the
// address of the resource or data block being walked, for self.
type readWalker struct {
	file    string
	top     string
	targets map[string][]*ChangedAttribute
	counted map[string]bool
	stack   []hclsyntax.Node
}

func (w *readWalker) Enter(n hclsyntax.Node) hcl.Diagnostics {
	if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok {
		w.reference(st)
	}
	w.stack = append(w.stack, n)
	return nil
}

func (w *readWalker) Exit(hclsyntax.Node) hcl.Diagnostics {
	w.stack = w.stack[:len(w.stack)-1]
	return nil
}

// reference records what one reference reads of a target block.
func (w *readWalker) reference(st *hclsyntax.ScopeTraversalExpr) {
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
	as, ok := w.targets[addr]
	if !ok || addr == "" {
		return
	}
	name, whole := readOf(steps, st, w.stack, w.counted[addr])
	line := st.SrcRange.Start.Line
	for _, a := range as {
		switch {
		case whole:
			a.Readers = append(a.Readers, Reader{Path: w.file, Line: line, How: HowObject})
		case name == a.Name && self:
			a.Readers = append(a.Readers, Reader{Path: w.file, Line: line, How: HowSelf})
		case name == a.Name:
			a.Readers = append(a.Readers, Reader{Path: w.file, Line: line, How: HowName})
		case a.Name == "tags" && name == "tags_all":
			a.Readers = append(a.Readers, Reader{Path: w.file, Line: line, How: HowTagsAll})
		}
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
			if strings.HasSuffix(name, ".tf.json") || e.Kind != KindFile {
				ds.Complete = false
				continue
			}
			body, _, err := parseSyntax(name, e.Data, lim)
			if err != nil {
				ds.Complete = false
				continue
			}
			for _, b := range body.Blocks {
				switch {
				case b.Type != "data":
				case len(b.Labels) == 2:
					ds.Types = append(ds.Types, b.Labels[0])
				default:
					ds.Complete = false
				}
			}
		}
		slices.Sort(ds.Types)
		ds.Types = slices.Compact(ds.Types)
		out = append(out, ds)
	}
	return out, nil
}
