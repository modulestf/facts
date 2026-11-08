package depth

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
	"github.com/zclconf/go-cty/cty"
)

// Item kinds of a configuration directory, and of the other files.
const (
	kResource  = "resource"
	kData      = "data"
	kModule    = "module"
	kVariable  = "variable"
	kOutput    = "output"
	kLocal     = "local"
	kProvider  = "provider"
	kTerraform = "terraform"
	kMoved     = "moved"
	kImport    = "import"
	kRemoved   = "removed"
	kCheck     = "check"
	kOther     = "other"  // any other top-level block, not modeled
	kTFVar     = "tfvar"  // one assignment of a variables file
	kFileAttr  = "attr"   // a top-level argument of another Terraform-read file
	kFileBlock = "fblock" // a top-level block of another Terraform-read file
)

// arg is one top-level argument or nested block type of an item. Nested
// blocks of one type are one arg; a dynamic block is "dynamic.<label>".
type arg struct {
	name  string
	canon string                 // layout-free text, for comparison
	exprs []hclsyntax.Expression // every expression in it, at any nesting
	attrs []string               // the attribute name of each expression, for the file sinks
	// scopes are the dynamic blocks around each expression, outermost
	// first, whose iterators it may read.
	scopes [][]dynScope
	names  []string // the names inside: its own, nested arguments, blocks and object keys
	// policy is the text of the parts whose names policyName matches, in
	// source order: such an argument, or a whole block of such a type or
	// label. policyKnown is set when it was computed, for the HCL form.
	policy      []string
	policyKnown bool
	line        int
}

// dynScope is one dynamic block: its iterator's name and its for_each.
type dynScope struct {
	name    string
	forEach hclsyntax.Expression // nil when it has none
}

// item is one block, one local or one assignment.
type item struct {
	kind   string
	labels []string
	addr   string // resource "t.n", "data.t.n", "module.m", "var.v", "output.o", "local.l", ...
	typ    string // resource or data type
	file   string
	dir    string
	line   int
	args   []*arg
	canon  string
}

func (it *item) arg(name string) *arg {
	for _, a := range it.args {
		if a.name == name {
			return a
		}
	}
	return nil
}

// reqProvider is one required_providers entry.
type reqProvider struct {
	local, source, version string
	hasSource, hasVersion  bool
}

// module is the configuration of one directory: its .tf and .tf.json files.
type module struct {
	dir    string
	items  []*item
	byAddr map[string]*item
	req    []reqProvider
	// reqBad is set when a required_providers entry cannot be read.
	reqBad bool
}

// fileModel is one Terraform-read file that is not configuration.
type fileModel struct {
	path  string
	form  string
	items []*item
}

// model is the parse of one tree.
type model struct {
	tree  *facts.Tree
	mods  map[string]*module    // configuration directories
	files map[string]*fileModel // every other Terraform-read file
	// bad maps each Terraform-read path that could not be parsed or walked
	// to why.
	bad map[string]string
}

// parseTree parses every Terraform-read file of t.
func parseTree(t *facts.Tree, lim facts.Limits) *model {
	m := &model{tree: t, mods: map[string]*module{}, files: map[string]*fileModel{}, bad: map[string]string{}}
	for _, p := range t.Paths {
		form := tfForm(p)
		if form == formNone {
			continue
		}
		e := t.Entries[p]
		if e.Kind != facts.KindFile {
			m.bad[p] = "not a regular file with known content: " + e.Kind
			continue
		}
		if form == formConfig {
			if isOverride(p) {
				m.bad[p] = "an override file"
				continue
			}
			mod := m.mods[path.Dir(p)]
			if mod == nil {
				mod = &module{dir: path.Dir(p), byAddr: map[string]*item{}}
				m.mods[mod.dir] = mod
			}
			var err string
			if isJSON(p) {
				err = jsonConfig(mod, p, e.Data, lim)
			} else {
				err = hclConfig(mod, p, e.Data, lim)
			}
			if err != "" {
				m.bad[p] = err
			}
			continue
		}
		fm := &fileModel{path: p, form: form}
		var err string
		if isJSON(p) {
			err = jsonFile(fm, e.Data, lim)
		} else {
			err = hclFile(fm, e.Data, lim)
		}
		if err != "" {
			m.bad[p] = err
			continue
		}
		m.files[p] = fm
	}
	for _, mod := range m.mods {
		for _, it := range mod.items {
			if mod.byAddr[it.addr] != nil {
				m.bad[it.file] = it.addr + " is declared twice"
			}
			mod.byAddr[it.addr] = it
		}
	}
	return m
}

// configDirs returns the configuration directories, sorted.
func (m *model) configDirs() []string {
	dirs := make([]string, 0, len(m.mods))
	for d := range m.mods {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	return dirs
}

// hclConfig adds the items of one .tf file.
func hclConfig(mod *module, file string, src []byte, lim facts.Limits) string {
	body, _, err := facts.ParseHCL(file, src, lim)
	if err != nil {
		return "does not parse"
	}
	if len(body.Attributes) > 0 {
		return "a top-level argument in a configuration file"
	}
	add := func(kind, addr, typ string, b *hclsyntax.Block) *item {
		it := &item{kind: kind, labels: b.Labels, addr: addr, typ: typ, file: file, dir: mod.dir,
			line: b.DefRange().Start.Line, args: blockArgs(b.Body, src), canon: normKey(b.Range().SliceBytes(src), true)}
		mod.items = append(mod.items, it)
		return it
	}
	for _, b := range body.Blocks {
		l := b.Labels
		switch {
		case b.Type == "resource" && len(l) == 2:
			add(kResource, l[0]+"."+l[1], l[0], b)
		case b.Type == "data" && len(l) == 2:
			add(kData, "data."+l[0]+"."+l[1], l[0], b)
		case b.Type == "module" && len(l) == 1:
			add(kModule, "module."+l[0], "", b)
		case b.Type == "variable" && len(l) == 1:
			add(kVariable, "var."+l[0], "", b)
		case b.Type == "output" && len(l) == 1:
			add(kOutput, "output."+l[0], "", b)
		case b.Type == "locals" && len(l) == 0:
			if len(b.Body.Blocks) > 0 {
				return "a block inside locals"
			}
			for _, a := range sortedAttrs(b.Body) {
				mod.items = append(mod.items, &item{kind: kLocal, addr: "local." + a.Name, file: file, dir: mod.dir,
					line: a.SrcRange.Start.Line, args: []*arg{attrArg(a, src)}, canon: normKey(a.SrcRange.SliceBytes(src), true)})
			}
		case b.Type == "provider" && len(l) == 1:
			it := add(kProvider, "", "", b)
			it.addr = "provider." + l[0] + "#" + it.canon
		case b.Type == "terraform" && len(l) == 0:
			it := add(kTerraform, "", "", b)
			it.addr = "terraform#" + it.canon
			mod.requiredHCL(b.Body)
		case b.Type == "moved" || b.Type == "import" || b.Type == "removed" || b.Type == "check":
			it := add(b.Type, "", "", b)
			it.addr = b.Type + "#" + it.canon
		default:
			it := add(kOther, "", "", b)
			it.addr = "other#" + it.canon
		}
	}
	return ""
}

// requiredHCL records the required_providers entries of a terraform block.
func (mod *module) requiredHCL(body *hclsyntax.Body) {
	for _, rb := range body.Blocks {
		if rb.Type != "required_providers" {
			continue
		}
		for _, a := range sortedAttrs(rb.Body) {
			rp := reqProvider{local: a.Name}
			switch x := a.Expr.(type) {
			case *hclsyntax.ObjectConsExpr:
				for _, kv := range x.Items {
					var ok bool
					switch keyName(kv.KeyExpr) {
					case "source":
						rp.source, ok = litString(kv.ValueExpr)
						rp.hasSource = true
					case "version":
						rp.version, ok = litString(kv.ValueExpr)
						rp.hasVersion = true
					default:
						ok = true
					}
					if !ok {
						mod.reqBad = true
					}
				}
			default:
				v, ok := litString(a.Expr)
				if !ok {
					mod.reqBad = true
				}
				rp.version, rp.hasVersion = v, true
			}
			mod.req = append(mod.req, rp)
		}
		if len(rb.Body.Blocks) > 0 {
			mod.reqBad = true
		}
	}
}

// hclFile reads a Terraform-read HCL file that is not configuration.
func hclFile(fm *fileModel, src []byte, lim facts.Limits) string {
	body, _, err := facts.ParseHCL(fm.path, src, lim)
	if err != nil {
		return "does not parse"
	}
	dir := path.Dir(fm.path)
	for _, a := range sortedAttrs(body) {
		kind, addr := kFileAttr, a.Name
		if fm.form == formVars {
			kind, addr = kTFVar, "var."+a.Name
		}
		fm.items = append(fm.items, &item{kind: kind, addr: addr, file: fm.path, dir: dir, line: a.SrcRange.Start.Line,
			args: []*arg{attrArg(a, src)}, canon: normKey(a.SrcRange.SliceBytes(src), true)})
	}
	if fm.form == formVars && len(body.Blocks) > 0 {
		return "a block in a variables file"
	}
	for _, b := range body.Blocks {
		fm.items = append(fm.items, &item{kind: kFileBlock, labels: b.Labels, addr: b.Type, file: fm.path, dir: dir,
			line: b.DefRange().Start.Line, args: blockArgs(b.Body, src), canon: normKey(b.Range().SliceBytes(src), true)})
	}
	return ""
}

// attrArg returns the arg of one attribute.
func attrArg(a *hclsyntax.Attribute, src []byte) *arg {
	out := &arg{name: a.Name, canon: normKey(a.Expr.Range().SliceBytes(src), false),
		exprs: []hclsyntax.Expression{a.Expr}, attrs: []string{a.Name}, scopes: [][]dynScope{nil}, names: append([]string{a.Name}, objectKeys(a.Expr)...),
		policyKnown: true, line: a.SrcRange.Start.Line}
	out.policy = policyPart(nil, "", a, src)
	return out
}

// policyName reports a name that may carry a policy: one holding policy, or
// an object key or block named Statement or Principal, in any case.
func policyName(n string) bool {
	l := strings.ToLower(n)
	return strings.Contains(l, "policy") || l == "statement" || l == "principal"
}

// policyPart adds to out the text of attribute a, at prefix, when its name or
// one of its object keys is a policy name.
func policyPart(out []string, prefix string, a *hclsyntax.Attribute, src []byte) []string {
	if policyName(a.Name) || slices.ContainsFunc(objectKeys(a.Expr), policyName) {
		out = append(out, prefix+a.Name+"="+normKey(a.Expr.Range().SliceBytes(src), false))
	}
	return out
}

// blockArgs returns the args of a block body, sorted by name.
func blockArgs(body *hclsyntax.Body, src []byte) []*arg {
	by := map[string]*arg{}
	for _, a := range body.Attributes {
		by[a.Name] = attrArg(a, src)
	}
	for _, nb := range body.Blocks {
		key := nb.Type
		if nb.Type == "dynamic" && len(nb.Labels) == 1 {
			key = "dynamic." + nb.Labels[0]
		}
		a := by[key]
		if a == nil {
			a = &arg{name: key, policyKnown: true, line: nb.DefRange().Start.Line}
			by[key] = a
		}
		a.canon += normKey(nb.Range().SliceBytes(src), true) + "\x01"
		collectBlock(nb, a, nil, src, "", false)
	}
	out := make([]*arg, 0, len(by))
	for _, a := range by {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y *arg) int { return cmp.Compare(x.name, y.name) })
	return out
}

// collectBlock adds every expression and name of a nested block to a.
// scope is the dynamic blocks around it; a dynamic block's content is in
// one more. prefix names the blocks around it, and inPolicy is set inside a
// block whose whole text is already in a.policy.
func collectBlock(nb *hclsyntax.Block, a *arg, scope []dynScope, src []byte, prefix string, inPolicy bool) {
	a.names = append(a.names, nb.Type)
	a.names = append(a.names, nb.Labels...)
	prefix += strings.Join(append([]string{nb.Type}, nb.Labels...), ".") + "/"
	if !inPolicy && (policyName(nb.Type) || slices.ContainsFunc(nb.Labels, policyName)) {
		a.policy = append(a.policy, prefix+normKey(nb.Range().SliceBytes(src), true))
		inPolicy = true
	}
	start := len(a.policy)
	inner := scope
	if nb.Type == "dynamic" && len(nb.Labels) == 1 {
		d := dynScope{name: nb.Labels[0]}
		if at := nb.Body.Attributes["iterator"]; at != nil {
			if n := hcl.ExprAsKeyword(at.Expr); n != "" {
				d.name = n
			}
		}
		if at := nb.Body.Attributes["for_each"]; at != nil {
			d.forEach = at.Expr
		}
		inner = append(slices.Clone(scope), d)
	}
	for _, at := range sortedAttrs(nb.Body) {
		in := inner
		if nb.Type == "dynamic" {
			in = scope // for_each, iterator and labels are outside the iteration
		}
		a.exprs = append(a.exprs, at.Expr)
		a.attrs = append(a.attrs, at.Name)
		a.scopes = append(a.scopes, in)
		a.names = append(a.names, at.Name)
		a.names = append(a.names, objectKeys(at.Expr)...)
		if !inPolicy {
			a.policy = policyPart(a.policy, prefix, at, src)
		}
	}
	for _, child := range nb.Body.Blocks {
		collectBlock(child, a, inner, src, prefix, inPolicy)
	}
	// A dynamic block decides how many copies of the policy parts inside it
	// there are, so its for_each and iterator are policy parts too.
	if nb.Type == "dynamic" && !inPolicy && len(a.policy) > start {
		meta := []string{prefix}
		for _, name := range []string{"for_each", "iterator"} {
			if at := nb.Body.Attributes[name]; at != nil {
				meta = append(meta, name+"="+normKey(at.Expr.Range().SliceBytes(src), false))
			}
		}
		a.policy = slices.Insert(a.policy, start, strings.Join(meta, "\x00"))
	}
}

// objectKeys returns the static keys of every object constructor in e.
func objectKeys(e hclsyntax.Expression) []string {
	var out []string
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if o, ok := n.(*hclsyntax.ObjectConsExpr); ok {
			for _, kv := range o.Items {
				if k := keyName(kv.KeyExpr); k != "" {
					out = append(out, k)
				}
			}
		}
		return nil
	})
	return out
}

// keyName returns the static name of an object key, or "".
func keyName(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		if name := hcl.ExprAsKeyword(k.Wrapped); name != "" {
			return name
		}
		e = k.Wrapped
	}
	s, _ := litString(e)
	return s
}

// litString returns the value of a string literal or of a template with no
// interpolation.
func litString(e hclsyntax.Expression) (string, bool) {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String && x.Val.IsKnown() && !x.Val.IsNull() {
			return x.Val.AsString(), true
		}
	case *hclsyntax.TemplateExpr:
		var b strings.Builder
		for _, p := range x.Parts {
			s, ok := litString(p)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	return "", false
}

func sortedAttrs(b *hclsyntax.Body) []*hclsyntax.Attribute {
	out := make([]*hclsyntax.Attribute, 0, len(b.Attributes))
	for _, a := range b.Attributes {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y *hclsyntax.Attribute) int {
		return cmp.Or(cmp.Compare(x.SrcRange.Start.Byte, y.SrcRange.Start.Byte), cmp.Compare(x.Name, y.Name))
	})
	return out
}

// normKey is the token sequence of src without comments and newlines, so
// that layout and comments are not a change. config lexes src as a file or
// block rather than one expression.
func normKey(src []byte, config bool) string {
	var toks hclsyntax.Tokens
	if config {
		toks, _ = hclsyntax.LexConfig(src, "", hcl.InitialPos)
	} else {
		toks, _ = hclsyntax.LexExpression(src, "", hcl.InitialPos)
	}
	var b strings.Builder
	for _, t := range toks {
		switch t.Type {
		case hclsyntax.TokenComment, hclsyntax.TokenNewline, hclsyntax.TokenEOF:
			continue
		}
		b.WriteString(t.Type.String())
		b.WriteByte(0)
		b.Write(t.Bytes)
		b.WriteByte(0)
	}
	return b.String()
}
