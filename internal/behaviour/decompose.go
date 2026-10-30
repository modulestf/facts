package behaviour

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// Item kinds of a module view.
const (
	itemResource   = "resource"
	itemData       = "data"
	itemModule     = "module"
	itemVariable   = "variable"
	itemOutput     = "output"
	itemLocal      = "local"
	itemProvider   = "provider"
	itemTFVersion  = "tf-version"
	itemTFProvider = "tf-provider"
	itemTFOther    = "tf-other"
	itemMoved      = "moved"
	itemOther      = "other"
)

const (
	reasonMoved     = "a moved or renamed address needs prior state, so it stays unverifiable"
	reasonApplyTime = "a provisioner or connection runs at apply time, which no plan shows, so it stays unverifiable"
)

// item is one top-level unit of a module directory: a block, one local, or
// one entry of a terraform block.
type item struct {
	key, kind, addr string
	typ             string // resource or data source type
	file            string // path in the tree
	src             []byte
	block           *hclsyntax.Block
	attr            *hclsyntax.Attribute // a local or a terraform block argument
	norm            string
	bodyNorm        string // the body alone, for rename detection
}

func (it *item) line() int {
	if it.attr != nil {
		return it.attr.SrcRange.Start.Line
	}
	return it.block.TypeRange.Start.Line
}

// view is the parsed configuration of one module directory at one revision.
type view struct {
	items     map[string]*item
	vars      map[string]string // variable name -> type constraint text
	providers map[string]string // local name -> source
}

// buildView parses the .tf files directly in dir. why is set when the
// directory cannot be decomposed: a file that is not a regular file, does
// not parse, or is an override file, or two units with one key.
func buildView(t *facts.Tree, dir string, lim facts.Limits) (v *view, why string) {
	v = &view{items: map[string]*item{}, vars: map[string]string{}, providers: map[string]string{}}
	if t == nil {
		return v, ""
	}
	for _, e := range t.DirEntries(dir) {
		name := path.Base(e.Path)
		if ignoredName(name) || !strings.HasSuffix(name, ".tf") {
			continue
		}
		if name == "override.tf" || strings.HasSuffix(name, "_override.tf") {
			return nil, e.Path + " is an override file, which merges into other blocks"
		}
		if e.Kind != facts.KindFile {
			return nil, e.Path + " is not a regular file with known content"
		}
		body, _, err := facts.ParseHCL(e.Path, e.Data, lim)
		if err != nil {
			return nil, e.Path + " does not parse"
		}
		for _, it := range fileItems(e.Path, e.Data, body, v) {
			if v.items[it.key] != nil {
				return nil, fmt.Sprintf("%s is declared twice", it.addr)
			}
			v.items[it.key] = it
		}
	}
	return v, ""
}

// fileItems splits one file into items, and records its variable types and
// provider sources in v.
func fileItems(file string, src []byte, body *hclsyntax.Body, v *view) []*item {
	var out []*item
	blk := func(b *hclsyntax.Block, key, kind, addr string) *item {
		it := &item{key: key, kind: kind, addr: addr, file: file, src: src, block: b,
			norm:     normKey(b.Range().SliceBytes(src), true),
			bodyNorm: normKey(b.Body.Range().SliceBytes(src), true)}
		out = append(out, it)
		return it
	}
	attrItem := func(a *hclsyntax.Attribute, key, kind, addr string) {
		out = append(out, &item{key: key, kind: kind, addr: addr, file: file, src: src, attr: a,
			norm: normKey(a.SrcRange.SliceBytes(src), true)})
	}
	for _, b := range body.Blocks {
		labels := strings.Join(b.Labels, ".")
		switch {
		case b.Type == "resource" && len(b.Labels) == 2:
			blk(b, "resource."+labels, itemResource, labels).typ = b.Labels[0]
		case b.Type == "data" && len(b.Labels) == 2:
			blk(b, "data."+labels, itemData, "data."+labels).typ = b.Labels[0]
		case b.Type == "module" && len(b.Labels) == 1:
			blk(b, "module."+labels, itemModule, "module."+labels)
		case b.Type == "variable" && len(b.Labels) == 1:
			blk(b, "variable."+labels, itemVariable, "var."+labels)
			v.vars[labels] = "any"
			if a := b.Body.Attributes["type"]; a != nil {
				v.vars[labels] = strings.Join(strings.Fields(string(a.Expr.Range().SliceBytes(src))), " ")
			}
		case b.Type == "output" && len(b.Labels) == 1:
			blk(b, "output."+labels, itemOutput, "output."+labels)
		case b.Type == "locals":
			for _, name := range sortedAttrNames(b.Body) {
				attrItem(b.Body.Attributes[name], "local."+name, itemLocal, "local."+name)
			}
		case b.Type == "provider" && len(b.Labels) == 1:
			addr := "provider." + labels
			if a := b.Body.Attributes["alias"]; a != nil {
				addr += "." + aliasName(a, src)
			}
			blk(b, addr, itemProvider, addr)
		case b.Type == "terraform":
			out = append(out, terraformItems(file, src, b, v)...)
		case b.Type == "moved":
			to := ""
			if a := b.Body.Attributes["to"]; a != nil {
				to = string(a.Expr.Range().SliceBytes(src))
			}
			blk(b, "moved:"+normKey(b.Body.Range().SliceBytes(src), true), itemMoved, to)
		default:
			addr := strings.TrimSuffix(b.Type+"."+labels, ".")
			blk(b, "other:"+normKey(b.Range().SliceBytes(src), true), itemOther, addr)
		}
	}
	return out
}

func aliasName(a *hclsyntax.Attribute, src []byte) string {
	if parts, ok := templateParts(a.Expr); ok {
		if s, static := literalPrefix(parts); static {
			return s
		}
	}
	return string(a.Expr.Range().SliceBytes(src))
}

// terraformItems splits a terraform block into required_version, one item
// per required provider, and one per other argument or nested block.
func terraformItems(file string, src []byte, b *hclsyntax.Block, v *view) []*item {
	var out []*item
	add := func(a *hclsyntax.Attribute, blk *hclsyntax.Block, key, kind string) {
		it := &item{key: key, kind: kind, addr: "terraform", file: file, src: src, attr: a, block: blk}
		if a != nil {
			it.norm = normKey(a.SrcRange.SliceBytes(src), true)
		} else {
			it.norm = normKey(blk.Range().SliceBytes(src), true)
		}
		out = append(out, it)
	}
	for _, name := range sortedAttrNames(b.Body) {
		kind := itemTFOther
		if name == "required_version" {
			kind = itemTFVersion
		}
		add(b.Body.Attributes[name], nil, "terraform:"+name, kind)
	}
	for _, nb := range b.Body.Blocks {
		if nb.Type != "required_providers" {
			add(nil, nb, "terraform:"+nb.Type+"."+strings.Join(nb.Labels, "."), itemTFOther)
			continue
		}
		for _, name := range sortedAttrNames(nb.Body) {
			a := nb.Body.Attributes[name]
			add(a, nil, "terraform:required_providers."+name, itemTFProvider)
			if src := objectField(a.Expr, "source"); src != nil {
				if parts, ok := templateParts(src); ok {
					if s, static := literalPrefix(parts); static {
						v.providers[name] = s
					}
				}
			}
		}
	}
	return out
}

// objectField returns the value of key in an object constructor.
func objectField(e hclsyntax.Expression, key string) hclsyntax.Expression {
	o, ok := unwrap(e).(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	for _, it := range o.Items {
		if k, ok := it.KeyExpr.(*hclsyntax.ObjectConsKeyExpr); ok {
			if st, ok := k.Wrapped.(*hclsyntax.ScopeTraversalExpr); ok && len(st.Traversal) == 1 && st.Traversal.RootName() == key {
				return it.ValueExpr
			}
			if parts, ok := templateParts(k.Wrapped); ok {
				if s, static := literalPrefix(parts); static && s == key {
					return it.ValueExpr
				}
			}
		}
	}
	return nil
}

// spec is a trigger being built.
type spec struct {
	file        string
	line        int // 0 for a change to a file as a whole
	cat, kind   string
	addr, attr  string
	b, h        *exprInfo
	reason      string
	noLevels    bool
	levels      []string // overrides the category's levels when set
	forEachOnly bool
}

// decomposer compares the base and head views of one directory.
type decomposer struct {
	c          *classifier
	base, head *view
	out        []spec
}

func (d *decomposer) add(s spec) { d.out = append(d.out, s) }

// compare emits a spec for every AST-level change of the directory.
func (d *decomposer) compare() {
	keys := map[string]bool{}
	for k := range d.base.items {
		keys[k] = true
	}
	for k := range d.head.items {
		keys[k] = true
	}
	var gone, added []*item
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		b, h := d.base.items[k], d.head.items[k]
		switch {
		case b == nil:
			added = append(added, h)
		case h == nil:
			gone = append(gone, b)
		case b.norm != h.norm:
			d.pair(b, h)
		}
	}
	gone, added = d.renames(gone, added)
	for _, b := range gone {
		d.alone(b, KindRemoved)
	}
	for _, h := range added {
		d.alone(h, KindAdded)
	}
}

// renames pairs a removed and an added resource, data source or module of
// one type with the same body: an address rename with no moved block.
func (d *decomposer) renames(gone, added []*item) ([]*item, []*item) {
	var left []*item
	for _, b := range gone {
		i := slices.IndexFunc(added, func(h *item) bool {
			return h.kind == b.kind && h.typ == b.typ && h.bodyNorm == b.bodyNorm &&
				(b.kind == itemResource || b.kind == itemData || b.kind == itemModule)
		})
		if i < 0 {
			left = append(left, b)
			continue
		}
		h := added[i]
		added = slices.Delete(added, i, i+1)
		d.add(spec{file: h.file, line: h.line(), cat: CatMoved, kind: KindRenamed, addr: h.addr,
			reason: reasonMoved, noLevels: true})
	}
	return left, added
}

// alone handles an item at one revision only.
func (d *decomposer) alone(it *item, kind string) {
	s := spec{file: it.file, line: it.line(), kind: kind, addr: it.addr}
	x := func(a *hclsyntax.Attribute) *exprInfo {
		if a == nil {
			return nil
		}
		return &exprInfo{a.Expr, it.src}
	}
	var e *exprInfo
	switch it.kind {
	case itemResource, itemData, itemModule:
		s.cat = CatResourceAddRemove
	case itemLocal:
		s.cat, e = CatLocalsOutputs, x(it.attr)
	case itemOutput:
		s.cat, s.attr, e = CatLocalsOutputs, "value", x(it.block.Body.Attributes["value"])
	case itemTFVersion:
		s.cat, s.attr, e = CatVersionFloor, "required_version", x(it.attr)
	case itemTFProvider:
		s.cat, s.attr, e = CatUnclassified, "required_providers."+it.attr.Name, x(it.attr)
	case itemMoved:
		s.cat, s.reason, s.noLevels = CatMoved, reasonMoved, true
	default:
		s.cat = CatUnclassified
	}
	if kind == KindRemoved {
		s.b = e
	} else {
		s.h = e
	}
	d.add(s)
}

// pair handles an item at both revisions whose source differs.
func (d *decomposer) pair(b, h *item) {
	switch b.kind {
	case itemResource, itemData:
		local := providerLocal(h)
		ctx := &resourceCtx{mode: b.kind, typ: b.typ, source: facts.ProviderKey(cmp.Or(d.head.providers[local], local))}
		d.body(b, h, b.block.Body, h.block.Body, nil, true, ctx)
	case itemLocal:
		bx, hx := &exprInfo{b.attr.Expr, b.src}, &exprInfo{h.attr.Expr, h.src}
		d.add(spec{file: h.file, line: h.line(), cat: classify(bx, hx, CatLocalsOutputs), kind: KindModified,
			addr: h.addr, b: bx, h: hx})
	case itemTFVersion:
		d.add(spec{file: h.file, line: h.line(), cat: CatVersionFloor, kind: KindModified, addr: "terraform",
			attr: "required_version", b: &exprInfo{b.attr.Expr, b.src}, h: &exprInfo{h.attr.Expr, h.src}})
	case itemTFProvider:
		cat := CatUnclassified
		if onlyVersionDiffers(b, h) {
			cat = CatVersionFloor
		}
		d.add(spec{file: h.file, line: h.line(), cat: cat, kind: KindModified, addr: "terraform",
			attr: "required_providers." + h.attr.Name, b: &exprInfo{b.attr.Expr, b.src}, h: &exprInfo{h.attr.Expr, h.src}})
	case itemTFOther, itemOther, itemMoved:
		d.add(spec{file: h.file, line: h.line(), cat: CatUnclassified, kind: KindModified, addr: h.addr})
	default: // module, variable, output, provider
		d.body(b, h, b.block.Body, h.block.Body, nil, true, nil)
	}
}

// onlyVersionDiffers reports a required provider whose source is unchanged
// and whose version constraint changed.
func onlyVersionDiffers(b, h *item) bool {
	key := func(it *item, field string) string {
		if e := objectField(it.attr.Expr, field); e != nil {
			return (&exprInfo{e, it.src}).key()
		}
		return ""
	}
	return key(b, "source") == key(h, "source") && key(b, "version") != key(h, "version")
}

// resourceCtx is the resource or data source a body belongs to.
type resourceCtx struct {
	mode, typ, source string
}

// providerLocal is the provider local name of a resource: its provider
// argument's first name, else its type's prefix.
func providerLocal(it *item) string {
	local, _, _ := strings.Cut(it.typ, "_")
	if a := it.block.Body.Attributes["provider"]; a != nil {
		if st, ok := a.Expr.(*hclsyntax.ScopeTraversalExpr); ok {
			local = st.Traversal.RootName()
		}
	}
	return local
}

// attrCategory is the category an argument of the item's kind forces, or ""
// to classify its expressions. skip is set for an argument whose change has
// no effect on behaviour.
func attrCategory(kind, name string, top bool) (cat string, skip bool) {
	if !top {
		return "", false
	}
	switch kind {
	case itemVariable:
		switch name {
		case "description":
			return "", true
		case "default":
			return CatDefault, false
		}
		return CatTypeValidation, false
	case itemOutput:
		if name == "description" {
			return "", true
		}
		if name != "value" {
			return CatLocalsOutputs, false
		}
	case itemModule:
		switch name {
		case "source", "version":
			return CatNonHCL, false
		case "depends_on", "providers":
			return CatUnclassified, false
		}
	case itemResource, itemData:
		if name == "depends_on" || name == "provider" {
			return CatUnclassified, false
		}
	}
	if name == "count" || name == "for_each" {
		return CatCountForEach, false
	}
	return "", false
}

// fallbackOf is the category of a changed expression of the item's kind
// that is no wrapper, conditional or literal change.
func fallbackOf(kind string) string {
	if kind == itemOutput || kind == itemLocal {
		return CatLocalsOutputs
	}
	return CatUnclassified
}

// classify picks the category of a changed expression.
func classify(b, h *exprInfo, fallback string) string {
	switch {
	case b != nil && h != nil && isWrapperChange(b, h):
		return CatWrapper
	case isConditional(b) || isConditional(h):
		return CatCountForEach
	case isLiteral(b) && isLiteral(h):
		return CatLiteral
	}
	return fallback
}

// body compares two bodies of an item, nested at prefix. ctx is set for a
// resource or data source body, whose attributes may be Optional+Computed.
func (d *decomposer) body(bi, hi *item, bb, hb *hclsyntax.Body, prefix []string, top bool, ctx *resourceCtx) {
	names := map[string]bool{}
	if bb != nil {
		for n := range bb.Attributes {
			names[n] = true
		}
	}
	if hb != nil {
		for n := range hb.Attributes {
			names[n] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		var bx, hx *exprInfo
		if bb != nil && bb.Attributes[name] != nil {
			bx = &exprInfo{bb.Attributes[name].Expr, bi.src}
		}
		if hb != nil && hb.Attributes[name] != nil {
			hx = &exprInfo{hb.Attributes[name].Expr, hi.src}
		}
		if bx != nil && hx != nil && bx.key() == hx.key() {
			continue
		}
		cat, skip := attrCategory(bi.kind, name, top)
		if skip {
			continue
		}
		forced := cat != ""
		if !forced {
			cat = classify(bx, hx, fallbackOf(bi.kind))
		}
		p := append(slices.Clone(prefix), name)
		s := d.at(bi, hi, bb, hb, name, bx, hx)
		s.cat, s.attr = cat, strings.Join(p, ".")
		if top && (name == "count" || name == "for_each") &&
			(bi.kind == itemResource || bi.kind == itemData || bi.kind == itemModule) {
			// The instances a block plans: terraform test plans them too.
			s.levels = []string{LevelL1, LevelL2}
		}
		d.add(s)
		s.levels = nil
		if ctx != nil && !forced && (hx == nil || !isLiteral(bx) || !isLiteral(hx)) {
			if emit, why := d.c.optionalComputed(ctx, p); emit {
				s.cat, s.reason = CatUnknownPropagation, why
				d.add(s)
			}
		}
	}
	d.blocks(bi, hi, bb, hb, prefix, top, ctx)
}

// at places a change to the argument name: at the head when it is there,
// else at the base.
func (d *decomposer) at(bi, hi *item, bb, hb *hclsyntax.Body, name string, bx, hx *exprInfo) spec {
	s := spec{addr: hi.addr, b: bx, h: hx, kind: KindModified}
	if hx == nil {
		s.file, s.line, s.kind = bi.file, bb.Attributes[name].SrcRange.Start.Line, KindRemoved
		return s
	}
	s.file, s.line = hi.file, hb.Attributes[name].SrcRange.Start.Line
	if bx == nil {
		s.kind = KindAdded
	}
	return s
}

// groupKey groups nested blocks for comparison.
func groupKey(b *hclsyntax.Block) string {
	switch {
	case b.Type == "dynamic" && len(b.Labels) == 1:
		return "dynamic." + b.Labels[0]
	case b.Type == "provisioner" && len(b.Labels) == 1:
		return "provisioner." + b.Labels[0]
	}
	return b.Type
}

func groups(b *hclsyntax.Body) map[string][]*hclsyntax.Block {
	g := map[string][]*hclsyntax.Block{}
	if b == nil {
		return g
	}
	for _, nb := range b.Blocks {
		g[groupKey(nb)] = append(g[groupKey(nb)], nb)
	}
	return g
}

// blocks compares the nested blocks of two bodies, group by group, in
// order within a group.
func (d *decomposer) blocks(bi, hi *item, bb, hb *hclsyntax.Body, prefix []string, top bool, ctx *resourceCtx) {
	bg, hg := groups(bb), groups(hb)
	keys := map[string]bool{}
	for k := range bg {
		keys[k] = true
	}
	for k := range hg {
		keys[k] = true
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		bl, hl := bg[k], hg[k]
		attr := strings.Join(append(slices.Clone(prefix), k), ".")
		first, kind, same := firstDiff(bi.src, hi.src, bl, hl)
		if same {
			continue
		}
		s := spec{addr: hi.addr, attr: attr, kind: kind}
		s.file, s.line = hi.file, 0
		if first.head != nil {
			s.line = first.head.TypeRange.Start.Line
		} else {
			s.file, s.line = bi.file, first.base.TypeRange.Start.Line
		}
		switch {
		case strings.HasPrefix(k, "dynamic."):
			s.cat = CatDynamic
			s.b, s.h = forEachOf(first.base, bi.src), forEachOf(first.head, hi.src)
			if onlyForEach(bi.src, hi.src, bl, hl) {
				// Only the for_each changed: L1 evaluates it.
				s.levels, s.forEachOnly = []string{LevelL1}, true
			}
			d.add(s)
		case strings.HasPrefix(k, "provisioner.") || k == "connection":
			s.cat, s.reason, s.noLevels = CatUnclassified, reasonApplyTime, true
			d.add(s)
		case k == "lifecycle" && top:
			d.flat(bi, hi, bodyOf(first.base), bodyOf(first.head), CatLifecycle, attr)
		case bi.kind == itemVariable && k == "validation" && top:
			s.cat = CatTypeValidation
			s.b, s.h = conditionOf(first.base, bi.src), conditionOf(first.head, hi.src)
			d.add(s)
		case bi.kind == itemOutput && top:
			s.cat = CatLocalsOutputs
			d.add(s)
		case first.base != nil && first.head != nil && (bi.kind == itemResource || bi.kind == itemData):
			for i := range min(len(bl), len(hl)) {
				d.body(bi, hi, bl[i].Body, hl[i].Body, append(slices.Clone(prefix), k), false, ctx)
			}
			for _, extra := range extras(bl, hl) {
				e := s
				e.kind = extra.kind
				if extra.head != nil {
					e.file, e.line = hi.file, extra.head.TypeRange.Start.Line
				} else {
					e.file, e.line = bi.file, extra.base.TypeRange.Start.Line
				}
				e.cat = CatUnclassified
				d.add(e)
			}
		default:
			s.cat = CatUnclassified
			d.add(s)
		}
	}
}

// onlyForEach reports two lists of one dynamic block group that differ
// only in the for_each of one block: same labels, iterator, content and
// every other argument and nested block.
func onlyForEach(bsrc, hsrc []byte, bl, hl []*hclsyntax.Block) bool {
	if len(bl) != len(hl) {
		return false
	}
	changed := 0
	for i := range bl {
		b, h := bl[i], hl[i]
		if normKey(b.Range().SliceBytes(bsrc), true) == normKey(h.Range().SliceBytes(hsrc), true) {
			continue
		}
		changed++
		bf, hf := b.Body.Attributes["for_each"], h.Body.Attributes["for_each"]
		if changed > 1 || bf == nil || hf == nil || !slices.Equal(b.Labels, h.Labels) ||
			withoutAttr(b.Body, "for_each", bsrc) != withoutAttr(h.Body, "for_each", hsrc) {
			return false
		}
	}
	return changed == 1
}

// withoutAttr is a key of a body's arguments but one, and its nested
// blocks.
func withoutAttr(b *hclsyntax.Body, skip string, src []byte) string {
	var parts []string
	for _, name := range sortedAttrNames(b) {
		if name != skip {
			parts = append(parts, name+"="+normKey(b.Attributes[name].Expr.Range().SliceBytes(src), false))
		}
	}
	for _, nb := range b.Blocks {
		parts = append(parts, normKey(nb.Range().SliceBytes(src), true))
	}
	return strings.Join(parts, "\x00")
}

// blockPair is one position of a block group at each revision.
type blockPair struct {
	base, head *hclsyntax.Block
	kind       string
}

// firstDiff returns the first position at which two block lists differ,
// and whether they are the same.
func firstDiff(bsrc, hsrc []byte, bl, hl []*hclsyntax.Block) (blockPair, string, bool) {
	for i := range max(len(bl), len(hl)) {
		var p blockPair
		if i < len(bl) {
			p.base = bl[i]
		}
		if i < len(hl) {
			p.head = hl[i]
		}
		switch {
		case p.base == nil:
			return p, KindAdded, false
		case p.head == nil:
			return p, KindRemoved, false
		case normKey(p.base.Range().SliceBytes(bsrc), true) != normKey(p.head.Range().SliceBytes(hsrc), true):
			return p, KindModified, false
		}
	}
	return blockPair{}, "", true
}

// extras are the blocks past the shorter of two lists.
func extras(bl, hl []*hclsyntax.Block) []blockPair {
	var out []blockPair
	for i := len(hl); i < len(bl); i++ {
		out = append(out, blockPair{base: bl[i], kind: KindRemoved})
	}
	for i := len(bl); i < len(hl); i++ {
		out = append(out, blockPair{head: hl[i], kind: KindAdded})
	}
	return out
}

func bodyOf(b *hclsyntax.Block) *hclsyntax.Body {
	if b == nil {
		return nil
	}
	return b.Body
}

func forEachOf(b *hclsyntax.Block, src []byte) *exprInfo {
	if b == nil || b.Body.Attributes["for_each"] == nil {
		return nil
	}
	return &exprInfo{b.Body.Attributes["for_each"].Expr, src}
}

func conditionOf(b *hclsyntax.Block, src []byte) *exprInfo {
	if b == nil || b.Body.Attributes["condition"] == nil {
		return nil
	}
	return &exprInfo{b.Body.Attributes["condition"].Expr, src}
}

// flat emits one spec of category cat per changed argument and per changed
// nested block group of two bodies, either of which may be missing.
func (d *decomposer) flat(bi, hi *item, bb, hb *hclsyntax.Body, cat, prefix string) {
	names := map[string]bool{}
	for _, b := range []*hclsyntax.Body{bb, hb} {
		if b != nil {
			for n := range b.Attributes {
				names[n] = true
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		var bx, hx *exprInfo
		if bb != nil && bb.Attributes[name] != nil {
			bx = &exprInfo{bb.Attributes[name].Expr, bi.src}
		}
		if hb != nil && hb.Attributes[name] != nil {
			hx = &exprInfo{hb.Attributes[name].Expr, hi.src}
		}
		if bx != nil && hx != nil && bx.key() == hx.key() {
			continue
		}
		s := d.at(bi, hi, bb, hb, name, bx, hx)
		s.cat, s.attr = cat, prefix+"."+name
		d.add(s)
	}
	bg, hg := groups(bb), groups(hb)
	keys := map[string]bool{}
	for k := range bg {
		keys[k] = true
	}
	for k := range hg {
		keys[k] = true
	}
	for _, k := range slices.Sorted(maps.Keys(keys)) {
		first, kind, same := firstDiff(bi.src, hi.src, bg[k], hg[k])
		if same {
			continue
		}
		s := spec{addr: hi.addr, attr: prefix + "." + k, kind: kind, cat: cat}
		if first.head != nil {
			s.file, s.line = hi.file, first.head.TypeRange.Start.Line
		} else {
			s.file, s.line = bi.file, first.base.TypeRange.Start.Line
		}
		d.add(s)
	}
}
