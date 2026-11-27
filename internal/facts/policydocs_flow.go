package facts

// Where the policy documents go: the sites of a directory, the flow of each
// site's expression, and the trace from a document to the blocks that use it.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/tffunc"
)

// Kinds of site: an expression a value can flow into.
const (
	siteGeneric     = iota // an argument of a block: the end of a trace
	siteLocal              // a local value
	siteMerge              // source_policy_documents or override_policy_documents of a merging document
	siteModuleInput        // an argument of a module call
	siteOutput             // the value of an output
)

// policySite is one expression of a directory and what flows into it.
type policySite struct {
	kind     int
	node     string // the node the site defines: local.X or output.X
	block    *policyBlock
	addr     string // the address of the block that holds the site
	argument string // the argument's path in the block
	role     string // siteMerge: HopSource or HopOverride
	call     string // siteModuleInput: the call's name
	child    string // siteModuleInput: the called directory, "" when outside the tree
	file     string
	src      []byte
	expr     hclsyntax.Expression
	items    []flowItem
	refs     []string // the addresses expr reads, sorted and unique
}

// flowItem is one node whose value reaches a site: at list depth rel of the
// site's value, flattened at depth flatAt (-1: never), or with an issue when
// it reaches it some way the reader cannot follow.
type flowItem struct {
	node   string
	rel    int
	flatAt int
	issue  string
}

// flowCtx is the site whose expression flowOf reads.
type flowCtx struct {
	di    *policyDirInfo
	block *policyBlock // the block that holds the site
	addr  string       // the document address prefix of the site
	file  string
	n     int // inline documents found so far in this site
}

// policyKey is the node a flow names for a document of a data block.
func policyKey(name string) string { return "data." + policyDocType + "." + name }

// coarseNode is the node a traversal reads, at any depth into it: a
// document, local, variable or module output; "module.C" for the whole
// module object; "" for anything else.
func coarseNode(t hcl.Traversal) string {
	names := []string{t.RootName()}
	for _, step := range t[1:] {
		switch s := step.(type) {
		case hcl.TraverseAttr:
			names = append(names, s.Name)
		case hcl.TraverseIndex:
			if names[0] == "module" && len(names) == 2 {
				continue // module.C[0].O
			}
			names = append(names, "[]")
		default:
			names = append(names, "[]")
		}
	}
	switch {
	case names[0] == "data" && len(names) >= 3 && names[1] == policyDocType:
		return policyKey(names[2])
	case (names[0] == "local" || names[0] == "var") && len(names) >= 2:
		return names[0] + "." + names[1]
	case names[0] == "module" && len(names) >= 3 && names[2] != "[]":
		return "module." + names[1] + "." + names[2]
	case names[0] == "module" && len(names) >= 2:
		return "module." + names[1]
	}
	return ""
}

// exactNode is the node a traversal reads whole: a document's json or
// minified_json, with a literal instance key or none; local.X; var.X;
// module.C.O, with a literal instance key or none. Any other traversal of a
// node is an index into it.
func exactNode(t hcl.Traversal) (string, bool) {
	var names []string
	var keys int
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			names = append(names, s.Name)
		case hcl.TraverseAttr:
			names = append(names, s.Name)
		case hcl.TraverseIndex:
			// An instance key is allowed after the name of a data block or
			// a module call only.
			if !s.Key.IsKnown() || s.Key.IsNull() || keys > 0 {
				return "", false
			}
			dataBlock := len(names) == 3 && names[0] == "data"
			moduleCall := len(names) == 2 && names[0] == "module"
			if !dataBlock && !moduleCall {
				return "", false
			}
			keys++
		default:
			return "", false
		}
	}
	switch {
	case len(names) == 4 && names[0] == "data" && names[1] == policyDocType && (names[3] == "json" || names[3] == "minified_json"):
		return policyKey(names[2]), true
	case len(names) == 2 && (names[0] == "local" || names[0] == "var") && keys == 0:
		return names[0] + "." + names[1], true
	case len(names) == 3 && names[0] == "module":
		return "module." + names[1] + "." + names[2], true
	}
	return "", false
}

// flowOf returns the nodes whose values reach expression e, which sits at
// list depth pos of the site's value, under a flatten at flatAt.
func (c *flowCtx) flowOf(e hclsyntax.Expression, pos, flatAt int) []flowItem {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return nil
	case *hclsyntax.ParenthesesExpr:
		return c.flowOf(x.Expression, pos, flatAt)
	case *hclsyntax.TemplateWrapExpr:
		return c.flowOf(x.Wrapped, pos, flatAt)
	case *hclsyntax.ScopeTraversalExpr:
		if n, ok := exactNode(x.Traversal); ok {
			return []flowItem{{node: n, rel: pos, flatAt: flatAt}}
		}
		return c.opaque(e, PolicyReasonIndex)
	case *hclsyntax.SplatExpr:
		if n, ok := docSplat(x); ok {
			return []flowItem{{node: n, rel: pos + 1, flatAt: flatAt}}
		}
		return c.opaque(e, PolicyReasonIndex)
	case *hclsyntax.TupleConsExpr:
		var out []flowItem
		for _, el := range x.Exprs {
			out = append(out, c.flowOf(el, pos+1, flatAt)...)
		}
		return out
	case *hclsyntax.FunctionCallExpr:
		switch {
		case x.ExpandFinal:
			return c.opaque(e, PolicyReasonFunction)
		case x.Name == "concat" || x.Name == "compact" || x.Name == "tolist":
			var out []flowItem
			for _, a := range x.Args {
				out = append(out, c.flowOf(a, pos, flatAt)...)
			}
			return out
		case x.Name == "flatten" && len(x.Args) == 1:
			f := pos
			if flatAt >= 0 && flatAt < f {
				f = flatAt
			}
			return c.flowOf(x.Args[0], pos, f)
		}
		if key, ok := c.inlineJSONEncode(x); ok {
			return append([]flowItem{{node: key, rel: pos, flatAt: flatAt}}, c.opaque(x.Args[0], PolicyReasonEmbedded)...)
		}
		return c.opaque(e, PolicyReasonFunction+" "+x.Name)
	case *hclsyntax.TemplateExpr:
		if key, ok := c.inlineJSONString(x); ok {
			out := []flowItem{{node: key, rel: pos, flatAt: flatAt}}
			for _, p := range x.Parts {
				out = append(out, c.opaque(p, PolicyReasonEmbedded)...)
			}
			return out
		}
		return c.opaque(e, PolicyReasonTemplate)
	case *hclsyntax.ConditionalExpr:
		return c.opaque(e, PolicyReasonConditional)
	case *hclsyntax.ForExpr:
		return c.opaque(e, PolicyReasonForExpression)
	case *hclsyntax.IndexExpr, *hclsyntax.RelativeTraversalExpr:
		return c.opaque(e, PolicyReasonIndex)
	}
	return c.opaque(e, PolicyReasonOtherExpression)
}

// docSplat reads data.aws_iam_policy_document.N[*].json.
func docSplat(x *hclsyntax.SplatExpr) (string, bool) {
	src, ok := x.Source.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(src.Traversal) != 3 {
		return "", false
	}
	n, ok := exactNode(append(slices.Clone(src.Traversal), hcl.TraverseAttr{Name: "json"}))
	if !ok {
		return "", false
	}
	each, ok := x.Each.(*hclsyntax.RelativeTraversalExpr)
	if !ok || len(each.Traversal) != 1 {
		return "", false
	}
	if _, ok := each.Source.(*hclsyntax.AnonSymbolExpr); !ok {
		return "", false
	}
	if a, ok := each.Traversal[0].(hcl.TraverseAttr); !ok || (a.Name != "json" && a.Name != "minified_json") {
		return "", false
	}
	return n, true
}

// opaque returns every node e reads, each with the issue: the reader
// cannot follow how its value reaches the site. Documents inside e are
// still found.
func (c *flowCtx) opaque(e hclsyntax.Expression, issue string) []flowItem {
	var out []flowItem
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch x := n.(type) {
		case *hclsyntax.ScopeTraversalExpr:
			if node := coarseNode(x.Traversal); node != "" {
				out = append(out, flowItem{node: node, flatAt: -1, issue: issue})
			}
		case *hclsyntax.FunctionCallExpr:
			if key, ok := c.inlineJSONEncode(x); ok {
				out = append(out, flowItem{node: key, flatAt: -1, issue: issue})
			}
		case *hclsyntax.TemplateExpr:
			if key, ok := c.inlineJSONString(x); ok {
				out = append(out, flowItem{node: key, flatAt: -1, issue: issue})
			}
		}
		return nil
	})
	return out
}

// inlineJSONEncode registers a jsonencode() call of an object with a
// Statement key as a document and returns its node.
func (c *flowCtx) inlineJSONEncode(x *hclsyntax.FunctionCallExpr) (string, bool) {
	if x.Name != "jsonencode" || len(x.Args) != 1 || x.ExpandFinal {
		return "", false
	}
	obj, ok := x.Args[0].(*hclsyntax.ObjectConsExpr)
	if !ok || !slices.ContainsFunc(obj.Items, func(it hclsyntax.ObjectConsItem) bool { return keyName(it.KeyExpr) == "Statement" }) {
		return "", false
	}
	key := c.register(PolicyKindJSONEncode, x.Range(), func() (*Policy, error) {
		n, err := hclJSON(x.Args[0], 0)
		if err != nil {
			return nil, err
		}
		return jsonPolicy(n)
	})
	return key, true
}

// inlineJSONString registers a string that holds a JSON policy as a
// document and returns its node.
func (c *flowCtx) inlineJSONString(x *hclsyntax.TemplateExpr) (string, bool) {
	text, interpolated, ok := jsonStringDoc(x)
	if !ok {
		return "", false
	}
	key := c.register(PolicyKindJSONString, x.Range(), func() (*Policy, error) {
		if interpolated {
			return nil, undecided(PolicyReasonInterpolatedJSON, "a JSON string with an interpolation or a directive")
		}
		n, err := textJSON(text)
		if err != nil {
			return nil, err
		}
		return jsonPolicy(n)
	})
	return key, true
}

// register adds an inline document of the site. Its address is the site's,
// with #2, #3 and on for a second and later one in the same site.
func (c *flowCtx) register(kind string, r hcl.Range, read func() (*Policy, error)) string {
	c.n++
	addr := c.addr
	if c.n > 1 {
		addr = fmt.Sprintf("%s#%d", c.addr, c.n)
	}
	d := &policyDocInfo{key: "doc:" + addr, block: c.block, doc: PolicyDoc{Dir: c.di.dir, Address: addr, Kind: kind, File: c.file,
		Lines: [2]int{r.Start.Line, r.End.Line}, Instances: InstancesAlways}}
	if p, err := read(); err != nil {
		d.doc.ContentUndecidable = reasonOf(err)
	} else {
		d.doc.Content = p
		d.refs = policyRefs(p)
	}
	c.di.docs = append(c.di.docs, d)
	return d.key
}

// policySink reports an argument that takes a policy document: policy,
// any name ending in _policy or policy_document, access_policies, and the
// content of an aws_organizations_policy. Any other argument that holds a
// policy is found only when it reads a document.
func policySink(blockType, name string) bool {
	return name == "policy" || name == "access_policies" || strings.HasSuffix(name, "_policy") ||
		strings.HasSuffix(name, "policy_document") || (blockType == "aws_organizations_policy" && name == "content")
}

// policyMergeArgs are the arguments of a merging document and the role of
// the hop through each: the lists and the legacy single JSON strings.
var policyMergeArgs = map[string]string{
	"source_policy_documents": HopSource, "override_policy_documents": HopOverride,
	"source_json": HopSource, "override_json": HopOverride,
}

// scanDocs finds the documents and sites of a directory. Uses are traced
// once every directory is scanned, since a trace crosses directories.
func (pt *policyTree) scanDocs(di *policyDirInfo) {
	for i := range di.blocks {
		pb := &di.blocks[i]
		b := pb.block
		switch {
		case b.Type == "locals":
			for _, a := range sortedAttrs(b.Body) {
				pt.addSite(di, policySite{kind: siteLocal, node: "local." + a.Name, block: pb, addr: "local." + a.Name, expr: a.Expr})
			}
		case b.Type == "output" && len(b.Labels) == 1:
			for _, a := range sortedAttrs(b.Body) {
				k := siteGeneric
				if a.Name == "value" {
					k = siteOutput
				}
				pt.addSite(di, policySite{kind: k, node: "output." + b.Labels[0], block: pb, addr: pb.addr, argument: a.Name, expr: a.Expr})
			}
			pt.walkBody(di, pb, &hclsyntax.Body{Blocks: b.Body.Blocks}, "", false)
		case b.Type == "module" && len(b.Labels) == 1:
			child, _ := policyLocalSource(di.dir, *pb)
			for _, a := range sortedAttrs(b.Body) {
				switch a.Name {
				case "source", "version", "depends_on", "providers":
				case "count", "for_each":
					pt.addSite(di, policySite{kind: siteGeneric, block: pb, addr: pb.addr, argument: a.Name, expr: a.Expr})
				default:
					pt.addSite(di, policySite{kind: siteModuleInput, block: pb, addr: pb.addr, argument: a.Name,
						call: b.Labels[0], child: child, expr: a.Expr})
				}
			}
			pt.walkBody(di, pb, &hclsyntax.Body{Blocks: b.Body.Blocks}, "", false)
		case b.Type == "data" && len(b.Labels) == 2 && b.Labels[0] == policyDocType:
			pt.addDataDoc(di, pb)
			for _, a := range sortedAttrs(b.Body) {
				role := policyMergeArgs[a.Name]
				switch {
				case role != "":
					pt.addSite(di, policySite{kind: siteMerge, block: pb, addr: pb.addr, argument: a.Name, role: role, expr: a.Expr})
				case a.Name != "depends_on":
					pt.addSite(di, policySite{kind: siteGeneric, block: pb, addr: pb.addr, argument: a.Name, expr: a.Expr})
				}
			}
			pt.walkBody(di, pb, &hclsyntax.Body{Blocks: b.Body.Blocks}, "", false)
		default:
			pt.walkBody(di, pb, b.Body, "", false)
		}
	}
	pt.addSinks(di)
	di.byNode = map[string][]siteItem{}
	for si := range di.sites {
		for ii, it := range di.sites[si].items {
			di.byNode[it.node] = append(di.byNode[it.node], siteItem{si, ii})
		}
	}
}

// siteItem is one flow item of a directory: its site and its index there.
type siteItem struct{ site, item int }

// itemsOf are the flow items that read a node, in source order: those
// that name it, and for a module output those that read the whole module
// object.
func (di *policyDirInfo) itemsOf(node string) (refs []siteItem, whole map[siteItem]bool) {
	refs = slices.Clone(di.byNode[node])
	if strings.HasPrefix(node, "module.") && strings.Count(node, ".") == 2 {
		whole = map[siteItem]bool{}
		for _, r := range di.byNode[node[:strings.LastIndex(node, ".")]] {
			whole[r] = true
			refs = append(refs, r)
		}
	}
	slices.SortFunc(refs, func(a, b siteItem) int {
		if a.site != b.site {
			return a.site - b.site
		}
		return a.item - b.item
	})
	return refs, whole
}

// walkBody adds a generic site for every argument of a body at any depth,
// but depends_on and the lifecycle block, which read no value into the
// block's configuration.
func (pt *policyTree) walkBody(di *policyDirInfo, pb *policyBlock, body *hclsyntax.Body, prefix string, dynamic bool) {
	for _, a := range sortedAttrs(body) {
		if a.Name == "depends_on" {
			continue
		}
		pt.addSite(di, policySite{kind: siteGeneric, block: pb, addr: pb.addr, argument: prefix + a.Name, expr: a.Expr, role: dynFlag(dynamic)})
	}
	counts := map[string]int{}
	for _, nb := range body.Blocks {
		if nb.Type == "lifecycle" {
			continue
		}
		if nb.Type == "dynamic" && len(nb.Labels) == 1 {
			p := prefix + nb.Labels[0] + "[dynamic]."
			for _, a := range sortedAttrs(nb.Body) {
				pt.addSite(di, policySite{kind: siteGeneric, block: pb, addr: pb.addr, argument: p + a.Name, expr: a.Expr, role: dynFlag(true)})
			}
			for _, cb := range nb.Body.Blocks {
				pt.walkBody(di, pb, cb.Body, p, true)
			}
			continue
		}
		i := counts[nb.Type]
		counts[nb.Type]++
		pt.walkBody(di, pb, nb.Body, fmt.Sprintf("%s%s[%d].", prefix, nb.Type, i), dynamic)
	}
}

// dynFlag marks a generic site inside a dynamic block: role is otherwise
// unused there.
func dynFlag(dynamic bool) string {
	if dynamic {
		return PolicyReasonDynamicBlock
	}
	return ""
}

// addSite reads the flow of a site's expression, which also finds the
// inline documents in it.
func (pt *policyTree) addSite(di *policyDirInfo, s policySite) {
	s.file, s.src = s.block.file, s.block.src
	docAddr := s.addr
	if s.argument != "" && s.kind != siteLocal {
		docAddr += "." + s.argument
	}
	c := &flowCtx{di: di, block: s.block, addr: docAddr, file: s.file}
	s.items = c.flowOf(s.expr, 0, -1)
	s.refs = slices.Compact(slices.Sorted(slices.Values(policyExprRefs(s.expr))))
	di.sites = append(di.sites, s)
}

// addDataDoc adds a data "aws_iam_policy_document" block as a document.
func (pt *policyTree) addDataDoc(di *policyDirInfo, pb *policyBlock) {
	b := pb.block
	r := b.Range()
	d := &policyDocInfo{key: policyKey(b.Labels[1]), doc: PolicyDoc{Dir: di.dir, Address: pb.addr, Kind: PolicyKindDataSource,
		File: pb.file, Lines: [2]int{r.Start.Line, r.End.Line}}}
	d.gate, d.block = blockGate(di.dir, pb), pb
	if d.gate != nil {
		d.doc.Gate = &PolicyGate{Meta: d.gate.Meta, Text: d.gate.Text}
	}
	d.doc.Instances = gateInstances(pb.block)
	switch p, err := dataSourcePolicy(b); {
	case len(di.byAddr[pb.addr]) > 1:
		d.doc.ContentUndecidable = &PolicyReason{Code: PolicyReasonDuplicate, Detail: pb.addr + " is defined twice"}
	case err != nil:
		d.doc.ContentUndecidable = reasonOf(err)
	default:
		d.doc.Content = p
		d.refs = policyRefs(p)
	}
	di.docs = append(di.docs, d)
}

// addSinks adds a document for each policy argument of a resource or data
// block whose value is not explained by documents alone.
func (pt *policyTree) addSinks(di *policyDirInfo) {
	for _, s := range di.sites {
		b := s.block.block
		typ := ""
		switch {
		case (b.Type == "resource" || b.Type == "data") && s.kind == siteGeneric && b.Labels[0] != policyDocType:
			typ = b.Labels[0]
		case b.Type == "provider" && s.kind == siteGeneric, b.Type == "module" && s.kind == siteModuleInput:
		default:
			continue
		}
		name := s.argument[strings.LastIndex(s.argument, ".")+1:]
		if !policySink(typ, name) {
			continue
		}
		if lit, ok := s.expr.(*hclsyntax.LiteralValueExpr); ok && lit.Val.IsNull() {
			continue
		}
		if pt.explained(di, s.items, 0) {
			continue
		}
		r := s.expr.Range()
		d := &policyDocInfo{key: "sink:" + s.addr + "." + s.argument, doc: PolicyDoc{Dir: di.dir, Address: s.addr + "." + s.argument,
			Kind: PolicyKindExpression, File: s.file, Lines: [2]int{r.Start.Line, r.End.Line}, Instances: InstancesAlways,
			ContentUndecidable: &PolicyReason{Code: PolicyReasonUnresolved, Detail: s.argument}}}
		g := blockGate(di.dir, s.block)
		u := PolicyUse{Dir: di.dir, Address: s.addr, Argument: s.argument, Via: []PolicyHop{}, Gates: []PolicyGate{}, Instances: gateInstances(b)}
		if g != nil {
			u.Gates = append(u.Gates, *g)
		}
		d.doc.Uses = []PolicyUse{u}
		d.sinkDefs = pt.blockDefs(di, s.block)
		di.docs = append(di.docs, d)
	}
}

// explained reports flow items that are all clean and all lead back to
// documents, through locals of the same directory.
func (pt *policyTree) explained(di *policyDirInfo, items []flowItem, depth int) bool {
	if len(items) == 0 || depth > 32 {
		return false
	}
	for _, it := range items {
		switch {
		case it.issue == PolicyReasonEmbedded:
			// read inside an inline document: part of its content
		case it.issue != "":
			return false
		case strings.HasPrefix(it.node, "local."):
			s := pt.localSite(di, it.node)
			if s == nil || !pt.explained(di, s.items, depth+1) {
				return false
			}
		case strings.HasPrefix(it.node, "var."), strings.HasPrefix(it.node, "module."):
			return false
		}
	}
	return true
}

func (pt *policyTree) localSite(di *policyDirInfo, node string) *policySite {
	for i := range di.sites {
		if di.sites[i].kind == siteLocal && di.sites[i].node == node {
			return &di.sites[i]
		}
	}
	return nil
}

// blockGate is the count or for_each of a top-level block, or nil.
func blockGate(dir string, pb *policyBlock) *PolicyGate {
	for _, m := range []string{"count", "for_each"} {
		if a, ok := pb.block.Body.Attributes[m]; ok {
			return &PolicyGate{Dir: dir, Address: pb.addr, Meta: m, Text: exprText(pb.src, a.Expr)}
		}
	}
	return nil
}

// gateInstances decides a block's instances from literal gates only.
func gateInstances(b *hclsyntax.Block) string {
	if a, ok := b.Body.Attributes["count"]; ok {
		if lit, ok := a.Expr.(*hclsyntax.LiteralValueExpr); ok && !lit.Val.IsNull() && lit.Val.IsKnown() && lit.Val.Type().FriendlyName() == "number" {
			if lit.Val.AsBigFloat().Cmp(new(big.Float)) == 0 {
				return InstancesNone
			}
			return InstancesAlways
		}
		return InstancesConditional
	}
	if a, ok := b.Body.Attributes["for_each"]; ok {
		switch x := a.Expr.(type) {
		case *hclsyntax.TupleConsExpr:
			if len(x.Exprs) == 0 {
				return InstancesNone
			}
			return InstancesAlways
		case *hclsyntax.ObjectConsExpr:
			if len(x.Items) == 0 {
				return InstancesNone
			}
			return InstancesAlways
		}
		return InstancesConditional
	}
	return InstancesAlways
}

func combineInstances(xs ...string) string {
	out := InstancesAlways
	for _, x := range xs {
		switch x {
		case InstancesNone:
			return InstancesNone
		case InstancesConditional:
			out = InstancesConditional
		}
	}
	return out
}

// traceState is one node a document's value has reached, on one path.
type traceState struct {
	dir   string
	node  string
	depth int
	via   []PolicyHop
	gates []PolicyGate
	insts []string
}

// key tells two states apart: the same node reached on paths with other
// hops or gates is another state, so no path's gates are lost.
func (st traceState) key() string {
	h := sha256.Sum256([]byte(canonJSON([]any{st.dir, st.node, st.depth, st.via, st.gates, st.insts})))
	return string(h[:])
}

// undecidedDef marks a definition a key cannot read; its key is then "".
const undecidedDef = "\x00undecided"

// finishDoc decides a document's content key and traces its uses.
func (pt *policyTree) finishDoc(di *policyDirInfo, d *policyDocInfo) {
	if di.undecidable != nil {
		r := &PolicyReason{Code: PolicyReasonDirectory, Detail: di.undecidable.Detail}
		d.doc.Content, d.doc.ContentUndecidable = nil, r
		d.doc.Uses, d.doc.UsesUndecidable = nil, r
		return
	}
	if d.doc.Content != nil {
		d.doc.contentKey = pt.contentKey(di, d)
	}
	var uses []PolicyUse
	var defs []string
	if d.doc.Kind == PolicyKindExpression {
		uses, defs = d.doc.Uses, []string{"sink:" + d.doc.Address, d.sinkDefs}
	} else {
		var reason *PolicyReason
		if uses, defs, reason = pt.trace(di, d); reason != nil {
			d.doc.UsesUndecidable = reason
			return
		}
		d.doc.Uses = uses
	}
	// The callers of every directory a use is in, up the call chain: their
	// count and for_each decide whether the use exists at all.
	for i, u := range uses {
		defs = append(defs, pt.callerDefs(u.Dir))
		if u.Instances == InstancesAlways && pt.chainInstances(u.Dir, 0) != InstancesAlways {
			uses[i].Instances = InstancesConditional
		}
	}
	if !slices.Contains(defs, undecidedDef) {
		slices.Sort(defs)
		d.doc.usesKey = hashKey(canonJSON(uses), strings.Join(slices.Compact(defs), "\x00"))
	}
}

// contentKey is the hash of a document's content and every definition its
// references read, or "" when one is not decided. A reference to path.* is
// not decided: its value depends on where Terraform runs. A reference to
// each.* or count.* reads the gate of the block that holds the document.
func (pt *policyTree) contentKey(di *policyDirInfo, d *policyDocInfo) string {
	parts := []string{canonJSON(d.doc.Content)}
	for _, r := range d.refs {
		switch root, _, _ := strings.Cut(r, "."); root {
		case "path":
			return ""
		case "each", "count":
			parts = append(parts, pt.gateDefs(di, d.block))
		}
	}
	parts = append(parts, pt.defs(di, d.refs, ""))
	if slices.Contains(parts, undecidedDef) {
		return ""
	}
	return hashKey(parts...)
}

// hashKey hashes the parts of a key: keys are only compared.
func hashKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// trace follows a document's value to every block that uses it, on every
// path: a node reached on two paths with other gates is followed twice.
func (pt *policyTree) trace(di *policyDirInfo, d *policyDocInfo) ([]PolicyUse, []string, *PolicyReason) {
	start := traceState{dir: di.dir, node: d.key, via: []PolicyHop{}, gates: []PolicyGate{}, insts: []string{d.doc.Instances}}
	var defs []string
	if d.gate != nil {
		start.gates = append(start.gates, *d.gate)
		defs = append(defs, pt.gateDefs(di, d.block))
	}
	var uses []PolicyUse
	seen := map[string]bool{}
	todo := []traceState{start}
	steps := 0
	for len(todo) > 0 {
		st := todo[0]
		todo = todo[1:]
		k := st.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		if len(st.via) > maxPolicyHops {
			return nil, nil, &PolicyReason{Code: PolicyReasonTooLarge, Detail: "over the hop limit"}
		}
		sd := pt.dirs[st.dir]
		if sd.undecidable != nil {
			return nil, nil, &PolicyReason{Code: PolicyReasonDirectory, Detail: st.dir}
		}
		refs, wholeRefs := sd.itemsOf(st.node)
		for _, ref := range refs {
			s, it := &sd.sites[ref.site], sd.sites[ref.site].items[ref.item]
			{
				if steps++; steps > maxPolicySteps {
					return nil, nil, &PolicyReason{Code: PolicyReasonTooLarge, Detail: "over the trace step limit"}
				}
				if steps%1024 == 0 && pt.lim.expired() != nil {
					return nil, nil, &PolicyReason{Code: PolicyReasonTooLarge, Detail: "past the deadline"}
				}
				// module.C reads every output of the call at once.
				whole := wholeRefs[ref]
				where := s.addr
				if s.argument != "" {
					where += "." + s.argument
				}
				if it.issue != "" || whole {
					code, detail := it.issue, where
					if whole {
						code = PolicyReasonIndex
					}
					if i := strings.IndexByte(code, ' '); i > 0 {
						code, detail = code[:i], code[i+1:]+" in "+where
					}
					return nil, nil, &PolicyReason{Code: code, Detail: detail}
				}
				abs := st.depth + it.rel
				if it.flatAt >= 0 && abs > it.flatAt {
					abs = it.flatAt + 1
				}
				// The site's text and every definition it reads. A data
				// source's own block is left out, as its content is compared
				// apart; an inline document has no block of its own, so the
				// text of every site and local on its way stays in.
				exclude := ""
				if d.doc.Kind == PolicyKindDataSource {
					exclude = di.dir + "|" + d.doc.Address
				}
				if !closedExpr(s.expr) {
					defs = append(defs, undecidedDef)
				} else {
					defs = append(defs, st.dir+"|"+where+"="+pt.fingerprint(s.src, s.expr.Range(), false),
						pt.defs(sd, s.refs, exclude))
				}
				next, use, more, reason := pt.step(st, sd, s, abs)
				if reason != nil {
					return nil, nil, reason
				}
				defs = append(defs, more...)
				if use != nil && (s.kind == siteGeneric || s.kind == siteOutput) {
					// The whole block that uses it: its target, provider,
					// lifecycle and gate are part of the use, and so is every
					// argument of an output.
					b := s.block
					if !closedBody(b.block.Body) {
						defs = append(defs, undecidedDef)
					}
					defs = append(defs, st.dir+"|"+b.addr+"="+pt.fingerprint(b.src, b.block.Range(), true),
						pt.defs(sd, bodyRefs(b.block.Body), exclude))
				}
				if use != nil {
					if uses = append(uses, *use); len(uses) > maxPolicyUses {
						return nil, nil, &PolicyReason{Code: PolicyReasonTooLarge, Detail: "over the use limit"}
					}
				}
				todo = append(todo, next...)
			}
		}
	}
	slices.SortFunc(uses, func(a, b PolicyUse) int { return strings.Compare(canonJSON(a), canonJSON(b)) })
	uses = slices.CompactFunc(uses, func(a, b PolicyUse) bool { return canonJSON(a) == canonJSON(b) })
	if uses == nil {
		uses = []PolicyUse{}
	}
	return uses, defs, nil
}

// policyExprRefs are the addresses an expression reads.
func policyExprRefs(e hclsyntax.Expression) []string {
	var out []string
	for _, t := range e.Variables() {
		if a := refAddress(t); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// with returns a copy of the state with more hops and gates.
func (st traceState) with(dir, node string, depth int, hops []PolicyHop, g *PolicyGate, inst string) traceState {
	n := traceState{dir: dir, node: node, depth: depth, via: append(slices.Clone(st.via), hops...), gates: slices.Clone(st.gates), insts: append(slices.Clone(st.insts), inst)}
	if n.via == nil {
		n.via = []PolicyHop{}
	}
	if g != nil {
		n.gates = append(n.gates, *g)
	}
	return n
}

// step moves a trace through one site. It returns the next states, or the
// use the site is, and the definitions the step reads.
func (pt *policyTree) step(st traceState, sd *policyDirInfo, s *policySite, abs int) ([]traceState, *PolicyUse, []string, *PolicyReason) {
	where := s.addr + "." + s.argument
	switch s.kind {
	case siteLocal:
		return []traceState{st.with(sd.dir, s.node, abs, []PolicyHop{{sd.dir, s.node, HopLocal}}, nil, InstancesAlways)}, nil, nil, nil
	case siteMerge:
		want := 1 // a list of documents
		if strings.HasSuffix(s.argument, "_json") {
			want = 0 // a legacy single document
		}
		if abs != want {
			return nil, nil, nil, &PolicyReason{Code: PolicyReasonShape, Detail: where}
		}
		key := policyKey(s.block.block.Labels[1])
		defs := []string{sd.dir + "|" + s.addr + "=" + pt.fingerprint(s.src, s.block.block.Range(), true), pt.gateDefs(sd, s.block)}
		return []traceState{st.with(sd.dir, key, 0, []PolicyHop{{sd.dir, s.addr, s.role}}, blockGate(sd.dir, s.block), gateInstances(s.block.block))}, nil, defs, nil
	case siteModuleInput:
		if s.child == "" || pt.dirs[s.child] == nil {
			return nil, nil, nil, &PolicyReason{Code: PolicyReasonExternalModule, Detail: where}
		}
		// The call itself is read by callerDefs of every use in the child,
		// and the child's sites read the variable's block.
		hops := []PolicyHop{{sd.dir, s.addr, HopModuleInput}, {s.child, "var." + s.argument, HopVariable}}
		return []traceState{st.with(s.child, "var."+s.argument, abs, hops, blockGate(sd.dir, s.block), gateInstances(s.block.block))}, nil, nil, nil
	case siteOutput:
		// A caller in a directory the reader cannot decide is unseen: the
		// output may be read there even when no caller is seen.
		if pt.anyUndec {
			return nil, nil, nil, &PolicyReason{Code: PolicyReasonCallerUnknown, Detail: where}
		}
		// The output is a use: callers outside the tree read it. In-tree
		// callers are traced on.
		u := PolicyUse{Dir: sd.dir, Address: s.addr, Argument: s.argument, Via: st.via, Gates: st.gates, Instances: combineInstances(st.insts...)}
		var next []traceState
		var defs []string
		for _, c := range pt.callers[sd.dir] {
			pb := &policyBlock{addr: "module." + c.name, file: c.file, src: c.src, block: c.block}
			node := "module." + c.name + "." + s.block.block.Labels[0]
			hops := []PolicyHop{{sd.dir, s.addr, HopOutput}, {c.parent, node, HopModuleOutput}}
			defs = append(defs, c.parent+"|module."+c.name+"="+pt.fingerprint(c.src, c.block.Range(), true),
				pt.defs(pt.dirs[c.parent], bodyRefs(c.block.Body), ""))
			if !closedBody(c.block.Body) {
				defs = append(defs, undecidedDef)
			}
			next = append(next, st.with(c.parent, node, abs, hops, blockGate(c.parent, pb), gateInstances(c.block)))
		}
		return next, &u, defs, nil
	}
	if abs != 0 {
		return nil, nil, nil, &PolicyReason{Code: PolicyReasonShape, Detail: where}
	}
	if s.role == PolicyReasonDynamicBlock {
		return nil, nil, nil, &PolicyReason{Code: PolicyReasonDynamicBlock, Detail: where}
	}
	if s.block.block.Type == "variable" {
		return nil, nil, nil, &PolicyReason{Code: PolicyReasonVariableDefault, Detail: where}
	}
	n := st.with(sd.dir, "", 0, nil, blockGate(sd.dir, s.block), gateInstances(s.block.block))
	u := PolicyUse{Dir: sd.dir, Address: s.addr, Argument: s.argument, Via: n.via, Gates: n.gates, Instances: combineInstances(n.insts...)}
	return nil, &u, nil, nil
}

// gateDefs is the text of a block's count or for_each and every definition
// it reads, or "" for a block with neither.
func (pt *policyTree) gateDefs(di *policyDirInfo, pb *policyBlock) string {
	for _, m := range []string{"count", "for_each"} {
		if a, ok := pb.block.Body.Attributes[m]; ok {
			if !closedExpr(a.Expr) {
				return undecidedDef
			}
			d := pt.defs(di, policyExprRefs(a.Expr), "")
			if d == undecidedDef {
				return d
			}
			return di.dir + "|" + pb.addr + "." + m + "=" + pt.fingerprint(pb.src, a.Expr.Range(), false) + "\x00" + d
		}
	}
	return ""
}

// blockDefs is the text of a whole block and every definition it reads, or
// undecidedDef.
func (pt *policyTree) blockDefs(di *policyDirInfo, pb *policyBlock) string {
	d := pt.defs(di, bodyRefs(pb.block.Body), "")
	if !closedBody(pb.block.Body) || d == undecidedDef {
		return undecidedDef
	}
	return di.dir + "|" + pb.addr + "=" + pt.fingerprint(pb.src, pb.block.Range(), true) + "\x00" + d
}

// chainInstances is what the gates of the in-tree calls of a directory, up
// the call chain, allow: always when every chain has only literal non-zero
// gates, conditional otherwise or when a caller may be unseen. Callers never
// make a use none: the directory may also be used directly.
func (pt *policyTree) chainInstances(dir string, depth int) string {
	cs := pt.callers[dir]
	if len(cs) == 0 {
		return InstancesAlways
	}
	if pt.anyUndec || depth > maxPolicyHops {
		return InstancesConditional
	}
	for _, c := range cs {
		if gateInstances(c.block) != InstancesAlways || pt.chainInstances(c.parent, depth+1) != InstancesAlways {
			return InstancesConditional
		}
	}
	return InstancesAlways
}

// policyPure are the functions a closure may call: their results depend on
// their arguments only. try and can are in it: every argument is read.
var policyPure = tffunc.Pure()

// closedExpr reports an expression in the closed grammar of a key: any
// syntax, calls to pure built-in functions only, and no path.* reference,
// whose value depends on where Terraform runs.
func closedExpr(e hclsyntax.Expression) bool {
	ok := true
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch x := n.(type) {
		case *hclsyntax.FunctionCallExpr:
			if !policyPure[x.Name] {
				ok = false
			}
		case *hclsyntax.ScopeTraversalExpr:
			if x.Traversal.RootName() == "path" {
				ok = false
			}
		}
		return nil
	})
	return ok
}

// closedBody reports a body whose every expression, nested blocks
// included, is closed.
func closedBody(b *hclsyntax.Body) bool {
	for _, a := range b.Attributes {
		if !closedExpr(a.Expr) {
			return false
		}
	}
	for _, nb := range b.Blocks {
		if !closedBody(nb.Body) {
			return false
		}
		// A dynamic block's iterator must be a bare name, as Terraform
		// requires; any other form leaves its references unscoped.
		if it, ok := nb.Body.Attributes["iterator"]; ok && nb.Type == "dynamic" && hcl.ExprAsKeyword(it.Expr) == "" {
			return false
		}
	}
	return true
}

// closedBlock reports a block whose body is closed. A variable's type is a
// type constraint, not an expression: list(string) calls no function and
// string names no definition. It must parse as one, with every optional
// default a constant; its text is in the block's fingerprint.
func closedBlock(b *hclsyntax.Block) bool {
	t, ok := b.Body.Attributes["type"]
	if b.Type != "variable" || !ok {
		return closedBody(b.Body)
	}
	if _, _, diags := typeexpr.TypeConstraintWithDefaults(t.Expr); diags.HasErrors() {
		return false
	}
	rest := *b.Body
	rest.Attributes = maps.Clone(b.Body.Attributes)
	delete(rest.Attributes, "type")
	return closedBody(&rest)
}

// bodyRefs are the addresses a body reads, nested blocks included.
func bodyRefs(b *hclsyntax.Body) []string {
	var out []string
	for _, t := range blockVariables(b) {
		if a := refAddress(t); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// policyEnvironmentType reports a resource or data type whose values come
// from the provider's API, which head and base share. Any other type may
// read the tree, run a program or make a value of its own.
func policyEnvironmentType(addr string) bool {
	t := strings.TrimPrefix(addr, "data.")
	return strings.HasPrefix(t, "aws_")
}

// readsFile reports a body with an argument that names a file to read, at
// any depth: source, filename, source_file, source_dir, source_path, or a
// name ending in _filename or _file_path. What such a file holds is not in
// any key.
func readsFile(b *hclsyntax.Body) bool {
	for name := range b.Attributes {
		switch {
		case name == "source", name == "filename", name == "source_file", name == "source_dir", name == "source_path",
			strings.HasSuffix(name, "_filename"), strings.HasSuffix(name, "_file_path"):
			return true
		}
	}
	return slices.ContainsFunc(b.Blocks, func(nb *hclsyntax.Block) bool { return readsFile(nb.Body) })
}

// callerDefs is the module calls of a directory in the tree, up the call
// chain: each whole call block and what it reads. A directory the reader
// cannot decide may hold another call.
func (pt *policyTree) callerDefs(dir string) string {
	w := pt.newDefsWalk()
	w.callers(dir)
	return w.result()
}

// defEntry is one definition of a closure: the hash of its key and the
// hash of its key and fingerprint.
type defEntry struct{ key, sum [32]byte }

// defs is the hash of the definitions the references of a directory read,
// transitively: locals, variables, resource and data blocks, and for a
// variable the module calls in the tree that set it, up the call chain.
// exclude, a "dir|address", leaves that definition's own text out; what it
// reads stays in. It is undecidedDef when a definition read is outside the
// closed grammar, is a module call output, a resource or data block of a
// type outside policyEnvironmentType, is written twice, or is a variable
// while any directory of the tree is undecidable.
func (pt *policyTree) defs(di *policyDirInfo, refs []string, exclude string) string {
	sorted := slices.Compact(slices.Sorted(slices.Values(refs)))
	memo := sha256.Sum256([]byte(di.dir + "\x00" + strings.Join(sorted, "\x00")))
	entries, ok := pt.defsMemo[memo]
	if !ok {
		w := pt.newDefsWalk()
		w.walk(di, sorted)
		entries = w.entries()
		pt.defsMemo[memo] = entries
		pt.entries += len(entries)
	}
	if entries == nil {
		return undecidedDef
	}
	ex := sha256.Sum256([]byte(exclude))
	h := sha256.New()
	for _, e := range entries {
		if exclude == "" || e.key != ex {
			h.Write(e.sum[:])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// defsWalk is one closure being read, across directories.
type defsWalk struct {
	pt   *policyTree
	seen map[string]bool
	out  []defEntry
	ok   bool
}

func (pt *policyTree) newDefsWalk() *defsWalk {
	return &defsWalk{pt: pt, seen: map[string]bool{}, ok: true}
}

func (w *defsWalk) add(key, fp string) {
	w.out = append(w.out, defEntry{key: sha256.Sum256([]byte(key)), sum: sha256.Sum256([]byte(key + "=" + fp))})
}

func (w *defsWalk) result() string {
	e := w.entries()
	if e == nil {
		return undecidedDef
	}
	h := sha256.New()
	for _, x := range e {
		h.Write(x.sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// entries are the definitions read sorted by their sums, or nil when one
// is undecided.
func (w *defsWalk) entries() []defEntry {
	if !w.ok || len(w.seen) > maxPolicyDefs {
		return nil
	}
	slices.SortFunc(w.out, func(a, b defEntry) int { return bytes.Compare(a.sum[:], b.sum[:]) })
	return append(make([]defEntry, 0, len(w.out)), w.out...) // never nil: nil is undecided
}

func (w *defsWalk) walk(di *policyDirInfo, refs []string) {
	todo := slices.Clone(refs)
	for len(todo) > 0 && w.ok {
		ref := todo[0]
		addr := refRoot(ref)
		todo = todo[1:]
		if addr == "" {
			// each and count are read through the block's gate, terraform
			// from the shared environment; any other root, such as an
			// iterator symbol, names no definition the key can read.
			switch ref[:strings.IndexAny(ref+".", ".[")] {
			case "each", "count", "terraform", "self":
				continue
			}
			w.ok = false
			return
		}
		key := di.dir + "|" + addr
		if w.seen[key] {
			continue
		}
		w.seen[key] = true
		if len(w.seen) > maxPolicyDefs {
			w.ok = false
			return
		}
		var more []string
		switch {
		case strings.HasPrefix(addr, "module."):
			w.ok = false
			return
		case strings.HasPrefix(addr, "local."):
			l := di.locals[strings.TrimPrefix(addr, "local.")]
			if l == nil {
				continue // no such local: the configuration does not load
			}
			if !closedExpr(l.attr.Expr) {
				w.ok = false
				return
			}
			w.add(key, w.pt.fingerprint(l.src, l.attr.Expr.Range(), false))
			more = policyExprRefs(l.attr.Expr)
		default:
			bs := di.byAddr[addr]
			switch len(bs) {
			case 0:
				// An iterator symbol, such as a dynamic block's, an ephemeral value,
				// which refAddress names by its type alone, or a block the
				// configuration lacks: nothing the key can read.
				w.ok = false
				return
			case 1:
			default:
				w.ok = false
				return
			}
			b := bs[0]
			if (b.block.Type == "resource" || b.block.Type == "data") && (!policyEnvironmentType(addr) || readsFile(b.block.Body)) {
				w.ok = false
				return
			}
			if !closedBlock(b.block) {
				w.ok = false
				return
			}
			w.add(key, w.pt.fingerprint(b.src, b.block.Range(), true))
			more = bodyRefs(b.block.Body)
			if strings.HasPrefix(addr, "var.") {
				w.callers(di.dir)
			}
		}
		todo = append(todo, more...)
	}
}

// callers adds the calls of a directory, up the call chain: each whole call
// block, its source, providers, arguments and gates, and what they read.
// With any undecidable directory a call may be unseen: not decided.
func (w *defsWalk) callers(dir string) {
	if w.pt.anyUndec {
		w.ok = false
		return
	}
	if w.seen["callers|"+dir] {
		return
	}
	w.seen["callers|"+dir] = true
	for _, c := range w.pt.callers[dir] {
		if !closedBody(c.block.Body) {
			w.ok = false
			return
		}
		w.add(dir+"<-"+c.parent+"|module."+c.name, w.pt.fingerprint(c.src, c.block.Range(), true))
		w.walk(w.pt.dirs[c.parent], bodyRefs(c.block.Body))
		w.callers(c.parent)
	}
}

// refRoot is the address a reference names: var.X, local.X, module.X,
// data.T.N or T.N; "" for path, terraform and other roots that name no
// definition of the directory.
func refRoot(r string) string {
	t, diags := hclsyntax.ParseTraversalAbs([]byte(r), "", hcl.InitialPos)
	if diags.HasErrors() {
		return r // already an address
	}
	return refAddress(t)
}

// blockVariables are the traversals of every expression of a body, but
// those rooted at the iterator of an enclosing dynamic block: its value is
// the block's own for_each, whose text and reads are in the same key.
func blockVariables(b *hclsyntax.Body) []hcl.Traversal {
	return scopedVariables(b, nil)
}

func scopedVariables(b *hclsyntax.Body, iters []string) []hcl.Traversal {
	var out []hcl.Traversal
	add := func(e hclsyntax.Expression, iters []string) {
		for _, t := range e.Variables() {
			if !slices.Contains(iters, t.RootName()) {
				out = append(out, t)
			}
		}
	}
	for _, a := range b.Attributes {
		add(a.Expr, iters)
	}
	for _, nb := range b.Blocks {
		if nb.Type != "dynamic" || len(nb.Labels) != 1 {
			out = append(out, scopedVariables(nb.Body, iters)...)
			continue
		}
		name := nb.Labels[0]
		if it, ok := nb.Body.Attributes["iterator"]; ok {
			if name = hcl.ExprAsKeyword(it.Expr); name == "" {
				// Not a name, which Terraform rejects and closedBody
				// refuses: read the whole block unscoped, for_each included.
				out = append(out, scopedVariables(nb.Body, iters)...)
				continue
			}
		}
		inner := append(slices.Clone(iters), name)
		for an, a := range nb.Body.Attributes {
			switch an {
			case "iterator":
			case "for_each":
				add(a.Expr, iters)
			default:
				add(a.Expr, inner)
			}
		}
		for _, cb := range nb.Body.Blocks {
			out = append(out, scopedVariables(cb.Body, inner)...)
		}
	}
	return out
}

// fingerprint is the package fingerprint, kept per file range so a closure
// read by many uses is hashed once. The cache holds 32-byte hashes.
func (pt *policyTree) fingerprint(src []byte, r hcl.Range, block bool) string {
	k := sha256.Sum256(fmt.Appendf(nil, "%p:%d:%d:%t", src, r.Start.Byte, r.End.Byte, block)) // one buffer per file
	if v, ok := pt.fps[k]; ok {
		return hex.EncodeToString(v[:])
	}
	v := fingerprint(src, r, block)
	b, _ := hex.DecodeString(v)
	pt.fps[k] = [32]byte(b)
	return v
}
