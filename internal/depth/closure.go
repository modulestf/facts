package depth

import (
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// maxNodes bounds the consumer closure.
const maxNodes = 100000

// node is one changed value: a variable, local, output, resource or data
// block (all its exported attributes), or a module call's outputs (out
// names one output, or "" for all).
type node struct{ dir, key, out string }

// consumer is one argument of one item that reads a value.
type consumer struct {
	it  *item
	a   *arg
	out string // for a module call reference, the output it reads; "" for any
}

// caller is one module call of a local module directory.
type caller struct{ dir, name string }

// analysis is the structural diff and the consumer closure.
type analysis struct {
	head, base *model
	links      *linker
	reasons    map[string]bool
	touched    map[string]bool // resource and data types
	blocks     int             // changed resource, data and module blocks
	seen       map[node]bool
	// policySame holds the direct changes whose policy parts are the same
	// at both revisions.
	policySame    map[*arg]bool
	queue         []node
	callers       map[string][]caller
	unknownCaller bool // a module call whose source does not resolve
	index         map[string]map[string][]consumer
	dirs          map[string]bool // directories the closure reaches
}

func newAnalysis(head, base *model, links *linker) *analysis {
	an := &analysis{head: head, base: base, links: links, reasons: map[string]bool{}, touched: map[string]bool{},
		seen: map[node]bool{}, policySame: map[*arg]bool{}, callers: map[string][]caller{}, index: map[string]map[string][]consumer{}, dirs: map[string]bool{}}
	for _, d := range head.configDirs() {
		idx := map[string][]consumer{}
		an.index[d] = idx
		for _, it := range head.mods[d].items {
			if it.kind == kModule {
				callee, local, ok := an.callee(it)
				switch {
				case !ok:
					an.unknownCaller = true
				case local:
					an.callers[callee] = append(an.callers[callee], caller{d, strings.TrimPrefix(it.addr, "module.")})
				}
			}
			for _, a := range it.args {
				for _, e := range a.exprs {
					for _, r := range refsOf(e) {
						idx[r.key] = append(idx[r.key], consumer{it, a, r.out})
					}
				}
			}
		}
	}
	return an
}

// ref is one value an expression reads: key names the block, variable or
// local, out the attribute or module output read first, "" for the whole
// object.
type ref struct{ key, out string }

// refsOf returns the values e reads. A splat or an index with an
// expression key keeps the attribute read after it.
func refsOf(e hclsyntax.Expression) []ref {
	var out []ref
	inner := map[*hclsyntax.ScopeTraversalExpr]string{}
	firstAttr := func(t hcl.Traversal) string {
		if len(t) > 0 {
			if a, ok := t[0].(hcl.TraverseAttr); ok {
				return a.Name
			}
		}
		return ""
	}
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch x := n.(type) {
		case *hclsyntax.SplatExpr:
			if st, ok := x.Source.(*hclsyntax.ScopeTraversalExpr); ok {
				if rt, ok := x.Each.(*hclsyntax.RelativeTraversalExpr); ok {
					inner[st] = firstAttr(rt.Traversal)
				}
			}
		case *hclsyntax.RelativeTraversalExpr:
			src := x.Source
			if ix, ok := src.(*hclsyntax.IndexExpr); ok {
				src = ix.Collection
			}
			if st, ok := src.(*hclsyntax.ScopeTraversalExpr); ok {
				inner[st] = firstAttr(x.Traversal)
			}
		}
		return nil
	})
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		st, ok := n.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			return nil
		}
		key, o, ok := refKey(st.Traversal)
		if !ok {
			return nil
		}
		if o == "" {
			o = inner[st]
		}
		if strings.HasPrefix(key, "var.") || strings.HasPrefix(key, "local.") {
			o = "" // a variable or local is traced as a whole value
		}
		out = append(out, ref{key, o})
		return nil
	})
	return out
}

// refKey returns the value a reference reads and the attribute or output
// read first after any index or splat.
func refKey(t hcl.Traversal) (key, out string, ok bool) {
	attr := func(i int) string {
		if i < len(t) {
			if a, ok := t[i].(hcl.TraverseAttr); ok {
				return a.Name
			}
		}
		return ""
	}
	root := t.RootName()
	switch root {
	case "path", "terraform", "count", "each", "self":
		return "", "", false
	case "var", "local":
		n := attr(1)
		return root + "." + n, "", n != ""
	case "module":
		n := attr(1)
		return "module." + n, after(t, 2), n != ""
	case "data":
		typ, n := attr(1), attr(2)
		return "data." + typ + "." + n, after(t, 3), typ != "" && n != ""
	}
	n := attr(1)
	return root + "." + n, after(t, 2), n != ""
}

// after returns the first attribute name at or after step i of t, past
// any index or splat steps; "" when there is none.
func after(t hcl.Traversal, i int) string {
	for ; i < len(t); i++ {
		switch s := t[i].(type) {
		case hcl.TraverseIndex, hcl.TraverseSplat:
			continue
		case hcl.TraverseAttr:
			return s.Name
		}
		return ""
	}
	return ""
}

// callee resolves a module call's source; see calleeOf.
func (an *analysis) callee(it *item) (dir string, local, ok bool) {
	return calleeOf(an.head, an.links, it)
}

// calleeOf resolves a module call's source. local is set for a local path;
// ok is false for a source that is not a literal or a local path that does
// not resolve to a configuration directory of the head.
func calleeOf(head *model, links *linker, it *item) (dir string, local, ok bool) {
	a := it.arg("source")
	if a == nil || len(a.exprs) != 1 {
		return "", false, false
	}
	src, lit := litString(a.exprs[0])
	if !lit {
		return "", false, false
	}
	src = strings.ReplaceAll(src, `\`, "/")
	if !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") {
		return "", false, true
	}
	dst, out, ok := links.join(it.dir, src)
	if out || !ok || head.mods[dst] == nil {
		return "", true, false
	}
	return dst, true, true
}

func (an *analysis) add(r string) { an.reasons[r] = true }

func (an *analysis) push(n node) {
	if !an.seen[n] {
		an.queue = append(an.queue, n)
	}
}

func (an *analysis) touch(typ string) {
	if typ != "" {
		an.touched[typ] = true
	}
}

// meta applies the deep classes an argument's name and its item force,
// and reports whether the argument is a meta-argument that ends the flow.
func (an *analysis) meta(it *item, a *arg, direct bool) bool {
	if wrappersPath(it.file) && (direct || it.kind != kOutput) {
		an.add(ReasonWrappers) // a wrapper output passing a module through is not a hit
	}
	if policyType(it) {
		an.add(ReasonPolicy)
	}
	// The keys of a type constraint name attributes of an input, not
	// values; a direct change whose policy parts are the same at both
	// revisions edits none of them.
	typeKeys := it.kind == kVariable && a.name == "type"
	if !typeKeys && (!direct || !an.policySame[a]) && slices.ContainsFunc(a.names, policyName) {
		an.add(ReasonPolicy)
	}
	switch n := a.name; {
	case n == "count" || n == "for_each":
		an.add(ReasonCountForEach)
	case n == "depends_on":
		an.add(ReasonDependsOn)
	case n == "provider" || n == "providers":
		an.add(ReasonProviderConfig)
	case n == "lifecycle" || n == "precondition" || n == "postcondition":
		an.add(ReasonLifecycleMovedRemovedImport)
	case n == "provisioner" || n == "connection":
		an.add(ReasonProvisioner)
	case strings.HasPrefix(n, "dynamic."):
		an.add(ReasonDynamicStructure)
	default:
		return false
	}
	return true
}

// flow handles a changed value in argument a of it: changed in the diff
// (direct), or read from another changed value.
func (an *analysis) flow(it *item, a *arg, direct bool) {
	unmodeled := func() {
		if direct {
			an.add(ReasonUnwalked)
		} else {
			an.add(ReasonUnresolvedConsumer)
		}
	}
	if !direct && a.name == "depends_on" {
		return // a reference in depends_on carries ordering, not a value
	}
	if an.meta(it, a, direct) {
		if label, ok := strings.CutPrefix(a.name, "dynamic."); ok && it.kind == kResource {
			an.touch(it.typ)
			an.push(node{it.dir, it.addr, label}) // the nested blocks it generates, as any other argument
		} else if it.kind == kResource || it.kind == kData {
			an.touch(it.typ)
			an.push(node{it.dir, it.addr, ""}) // every reference to it is affected
		}
		if !direct && (a.name == "precondition" || a.name == "postcondition") {
			an.add(ReasonUnresolvedConsumer)
		}
		if !direct && a.name == "lifecycle" && slices.ContainsFunc(a.names, func(n string) bool {
			return n == "precondition" || n == "postcondition"
		}) {
			an.add(ReasonUnresolvedConsumer)
		}
		return
	}
	switch it.kind {
	case kResource:
		an.touch(it.typ)
		if identityRe.MatchString(a.name) {
			an.push(node{it.dir, it.addr, ""}) // an identity: every computed attribute, id and arn included
		} else {
			an.push(node{it.dir, it.addr, a.name}) // the attribute of the same name
		}
	case kData:
		an.touch(it.typ)
		an.push(node{it.dir, it.addr, ""}) // a query: every exported attribute is its result
	case kLocal:
		an.push(node{it.dir, it.addr, ""})
	case kTFVar:
		if mod := an.head.mods[it.dir]; mod == nil || mod.byAddr[it.addr] == nil || mod.byAddr[it.addr].kind != kVariable {
			an.add(ReasonUnresolvedConsumer) // no module here declares the variable: who reads the file is unknown
			return
		}
		an.push(node{it.dir, it.addr, ""})
	case kOutput:
		switch a.name {
		case "value":
			an.push(node{it.dir, it.addr, ""})
		case "description":
		case "sensitive", "ephemeral":
			an.add(ReasonOutputSensitive)
		default:
			unmodeled()
		}
	case kModule:
		if a.name == "source" || a.name == "version" {
			an.add(ReasonModuleSourceVersion)
			return
		}
		callee, local, ok := an.callee(it)
		if !ok || !local {
			an.add(ReasonUnresolvedConsumer)
			return
		}
		an.push(node{callee, "var." + a.name, ""})
	case kVariable:
		if !direct {
			an.add(ReasonUnresolvedConsumer)
		} else if a.name != "description" {
			an.add(ReasonVariableContract)
		}
	case kProvider:
		an.add(ReasonProviderConfig)
	case kTerraform:
		an.add(ReasonTerraformBlock)
	case kMoved, kImport, kRemoved:
		an.add(ReasonLifecycleMovedRemovedImport)
		if !direct {
			an.add(ReasonUnresolvedConsumer)
		}
	case kFileAttr, kFileBlock:
		an.add(ReasonUnresolvedFileDependency)
	default:
		unmodeled()
	}
}

// close runs the consumer closure over every queued value.
func (an *analysis) close() {
	for len(an.queue) > 0 {
		n := an.queue[0]
		an.queue = an.queue[1:]
		if an.seen[n] {
			continue
		}
		an.seen[n] = true
		if len(an.seen) > maxNodes {
			an.add(ReasonUnwalked)
			return
		}
		an.dirs[n.dir] = true
		if out, ok := strings.CutPrefix(n.key, "output."); ok {
			if an.unknownCaller {
				an.add(ReasonUnresolvedConsumer)
			}
			for _, c := range an.callers[n.dir] {
				an.push(node{c.dir, "module." + c.name, out})
			}
			continue
		}
		for _, c := range an.index[n.dir][n.key] {
			if n.out != "" && c.out != "" && c.out != n.out {
				continue
			}
			an.flow(c.it, c.a, false)
		}
	}
}

// Kinds compared as a whole per directory, and the reason a change forces.
var wholeKinds = map[string]string{
	kTerraform: ReasonTerraformBlock,
	kProvider:  ReasonProviderConfig,
	kMoved:     ReasonLifecycleMovedRemovedImport,
	kImport:    ReasonLifecycleMovedRemovedImport,
	kRemoved:   ReasonLifecycleMovedRemovedImport,
	kCheck:     ReasonUnwalked,
	kOther:     ReasonUnwalked,
}

// diffDir compares the configuration of one directory at both revisions.
func (an *analysis) diffDir(dir string) {
	var bItems, hItems []*item
	if m := an.base.mods[dir]; m != nil {
		bItems = m.items
	}
	if m := an.head.mods[dir]; m != nil {
		hItems = m.items
	}
	whole := func(items []*item, kind string) []string {
		var out []string
		for _, it := range items {
			if it.kind == kind {
				out = append(out, it.canon)
			}
		}
		slices.Sort(out)
		return out
	}
	for kind, reason := range wholeKinds {
		if !slices.Equal(whole(bItems, kind), whole(hItems, kind)) {
			an.add(reason)
		}
	}
	byAddr := func(items []*item) map[string]*item {
		m := map[string]*item{}
		for _, it := range items {
			if _, w := wholeKinds[it.kind]; !w {
				m[it.addr] = it
			}
		}
		return m
	}
	bm, hm := byAddr(bItems), byAddr(hItems)
	addrs := map[string]bool{}
	for a := range bm {
		addrs[a] = true
	}
	for a := range hm {
		addrs[a] = true
	}
	for _, addr := range slices.Sorted(maps.Keys(addrs)) {
		b, h := bm[addr], hm[addr]
		switch {
		case h == nil:
			an.removed(b)
		case b == nil:
			an.added(h, bItems)
		case b.canon != h.canon:
			if h.kind == kResource || h.kind == kData || h.kind == kModule {
				an.blocks++
			}
			an.touch(h.typ)
			an.diffArgs(b, h)
		}
	}
}

func (an *analysis) removed(b *item) {
	switch b.kind {
	case kResource, kData, kModule:
		an.blocks++
		an.touch(b.typ)
		an.add(ReasonBlockRemovedOrRenamed)
	case kOutput, kVariable:
		an.add(ReasonBlockRemovedOrRenamed)
	}
}

func (an *analysis) added(h *item, base []*item) {
	switch h.kind {
	case kVariable:
		an.add(ReasonVariableContract)
		return
	case kResource, kData:
		an.blocks++
		an.touch(h.typ)
		if !slices.ContainsFunc(base, func(b *item) bool { return b.kind == h.kind && b.typ == h.typ }) {
			an.add(ReasonNewType)
		}
		an.push(node{h.dir, h.addr, ""})
	case kModule:
		an.blocks++
		an.add(ReasonModuleSourceVersion)
	}
	for _, a := range h.args {
		if h.kind == kOutput && (a.name == "sensitive" || a.name == "ephemeral" || a.name == "description") {
			continue // a new output changes no existing contract
		}
		an.flow(h, a, true)
	}
}

// diffArgs flows every argument that differs between two revisions of an
// item.
func (an *analysis) diffArgs(b, h *item) {
	names := map[string]bool{}
	for _, a := range b.args {
		names[a.name] = true
	}
	for _, a := range h.args {
		names[a.name] = true
	}
	for _, n := range slices.Sorted(maps.Keys(names)) {
		ba, ha := b.arg(n), h.arg(n)
		if ba != nil && ha != nil && ba.canon == ha.canon {
			continue
		}
		if ha == nil {
			ha = ba
		}
		if ba != nil && ha != ba && ba.policyKnown && ha.policyKnown && slices.Equal(ba.policy, ha.policy) {
			an.policySame[ha] = true
		}
		an.flow(h, ha, true)
	}
}

// diffFile compares the items of a variables file at both revisions.
func (an *analysis) diffFile(b, h *fileModel) {
	bm, hm := map[string]*item{}, map[string]*item{}
	if b != nil {
		for _, it := range b.items {
			bm[it.addr] = it
		}
	}
	if h != nil {
		for _, it := range h.items {
			hm[it.addr] = it
		}
	}
	for _, it := range hm {
		if o := bm[it.addr]; o == nil || o.canon != it.canon {
			an.flow(it, it.args[0], true)
		}
	}
	for _, it := range bm {
		if hm[it.addr] == nil {
			an.flow(it, it.args[0], true)
		}
	}
}

// policyType reports an IAM resource or data source, a policy or
// permission resource, or a policy document data source: a type with an
// _iam_ segment, ending in _policy or _permission, or a data source type
// ending in _policy_document.
func policyType(it *item) bool {
	t := strings.ToLower(it.typ)
	return strings.Contains(t, "_iam_") || strings.HasSuffix(t, "_iam") ||
		strings.HasSuffix(t, "_policy") || strings.HasSuffix(t, "_permission") ||
		it.kind == kData && strings.HasSuffix(t, "_policy_document")
}

// identityRe matches a managed resource argument that defines its identity.
// A change to one affects every computed attribute (id, arn and the
// like), not only the attribute of the same name. A heuristic: the plan of
// every selected example still shows a replacement.
var identityRe = regexp.MustCompile(`^(name|name_prefix|bucket|bucket_prefix|identifier|identifier_prefix|cluster_identifier|cluster_name|function_name|family|[a-z0-9_]*_name|[a-z0-9_]*_identifier)$`)
