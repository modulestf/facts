package facts

import (
	"cmp"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Anchors of a read site: what its path is relative to.
const (
	AnchorModule = "module" // the directory of the module that holds the site, path.module
	AnchorRoot   = "root"   // the root module of a run: the repository root or an example
	AnchorNone   = "none"   // unknown: the site may read any path
)

// docExts are the extensions of documentation files, at any depth.
var docExts = []string{".md", ".markdown", ".rst", ".adoc"}

// docNames are repository metadata files, at any depth.
var docNames = map[string]bool{
	"LICENSE": true, "LICENSE.txt": true, "NOTICE": true, "CODEOWNERS": true, ".gitignore": true,
	".gitattributes": true, ".editorconfig": true, ".pre-commit-config.yaml": true, ".releaserc.json": true,
	".releaserc": true, ".terraform-docs.yml": true, ".markdownlint.json": true, ".markdownlint.yaml": true,
}

// docDirs are top-level directories of documentation and repository
// metadata: everything under them is inert unless it is configuration.
var docDirs = []string{"docs/", ".github/"}

// InertDocPath reports whether p is an enumerated documentation or metadata
// path, which no plan reads unless an HCL read site reaches it; see
// ReadSites. Configuration, variable, test and lock files never are.
func InertDocPath(p string) bool {
	base := path.Base(p)
	for _, ext := range []string{".tf", ".tf.json", ".tfvars", ".tfvars.json", ".hcl"} {
		if strings.HasSuffix(base, ext) {
			return false
		}
	}
	if docNames[base] {
		return true
	}
	for _, ext := range docExts {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	for _, d := range docDirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

// ReadSite is one place in the HCL of a tree that may read a file: the first
// argument of a file function, an attribute a resource, data source or
// provisioner reads a file or runs a program from, or any other string that
// is a path, anchored at path.module, path.root or path.cwd, or a plain
// literal other than an IAM path that goes only to IAM path arguments (see
// iamPathText). Text is the static part of the path after its anchor; Exact
// is set when the path has no dynamic part.
type ReadSite struct {
	File   string // tree path of the .tf file
	Line   int
	Kind   string // the function or attribute name, or "path" for any other string
	Anchor string // AnchorModule, AnchorRoot or AnchorNone
	Dir    string // the directory of the module that holds the site
	Text   string
	Exact  bool
}

// readFuncs are the functions whose first argument is a path they read.
var readFuncs = map[string]bool{
	"file": true, "filebase64": true, "filemd5": true, "filesha1": true, "filesha256": true, "filesha512": true,
	"filebase64sha256": true, "filebase64sha512": true, "fileexists": true, "fileset": true, "templatefile": true,
}

// How a read attribute names what it reads.
const (
	readsPath  = 1 // a path
	readsShell = 2 // a command line or program a shell or the OS runs: it may read any file
)

// readAttrs are, by resource or data source type, or by provisioner type, the
// attributes that name a file the plan or apply reads or a program it runs.
var readAttrs = map[string]map[string]int{
	"local_file":           {"filename": readsPath, "source": readsPath},
	"local_sensitive_file": {"filename": readsPath, "source": readsPath},
	"archive_file":         {"source_file": readsPath, "source_dir": readsPath},
	"external":             {"program": readsShell, "working_dir": readsPath},
	"local-exec":           {"command": readsShell, "working_dir": readsPath, "interpreter": readsShell},
	"remote-exec":          {"script": readsPath, "scripts": readsPath},
	"file":                 {"source": readsPath},
}

// defaultMaxReadSites bounds the read sites of one tree; a tree with more is
// treated as one that does not parse. Tests lower it through Limits.
const defaultMaxReadSites = 100000

func (l Limits) readSitesCap() int { return cmp.Or(l.maxReadSites, defaultMaxReadSites) }

// ReadSites lists the read sites of every *.tf file of t. complete is false
// when a file cannot be parsed or the tree has more than its cap of sites,
// so a path no site reaches may still be read. It stops with ErrDeadline
// when lim's deadline passes.
func ReadSites(t *Tree, lim Limits) (sites []ReadSite, complete bool, err error) {
	var held []heldSite
	refs := map[string]bool{}
	uses := flowUses{}
	overrides := map[string]bool{}
	for _, p := range t.Paths {
		if isOverride(path.Base(p)) {
			overrides[path.Dir(p)] = true
		}
	}
	for _, p := range t.Paths {
		if err := lim.expired(); err != nil {
			return nil, false, err
		}
		name := path.Base(p)
		if ignoredName(name) || (!strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tf.json")) {
			continue
		}
		e := t.Entries[p]
		if e.Kind != KindFile || strings.HasSuffix(name, ".tf.json") {
			return nil, false, nil
		}
		body, _, err := parseBody(name, e.Data, lim)
		if err != nil {
			return nil, false, nil
		}
		s := siteScan{dir: path.Dir(p), file: p, refs: refs, uses: uses, overrides: overrides[path.Dir(p)]}
		s.body(body, "", "", "", false)
		held = append(held, s.held...)
		if sites = append(sites, s.sites...); len(sites) > lim.readSitesCap() {
			return nil, false, nil
		}
	}
	for _, h := range held {
		steps := 0
		blocks, ok := uses.sinks(t, h.to, map[flowNode]bool{}, &steps)
		if !ok || slices.ContainsFunc(blocks, func(b string) bool { return refs[b] }) {
			sites = append(sites, h.sites...)
		}
	}
	sites, err = templateSites(t, sites, lim)
	if err != nil {
		return nil, false, err
	}
	return sites, len(sites) <= lim.readSitesCap(), nil
}

// templateSites adds the read sites inside the templates templatefile reads:
// a file function called in a template reads a path relative to the root
// module, or any path when its argument is a template variable. A template
// that does not parse may read any file. Each site is the module's that
// calls templatefile. A templatefile site that reaches every file adds
// nothing.
func templateSites(t *Tree, sites []ReadSite, lim Limits) ([]ReadSite, error) {
	roots := exampleRoots(t)
	n := len(sites)
	for _, call := range sites[:n] {
		if call.Kind != "templatefile" || call.Anchor == AnchorNone {
			continue
		}
		for _, p := range t.Paths {
			if err := lim.expired(); err != nil {
				return nil, err
			}
			e := t.Entries[p]
			if !call.Reaches(p, roots) {
				continue
			}
			s := siteScan{dir: call.Dir, file: p}
			if e.Kind != KindFile || e.opaque {
				s.sites = append(s.sites, ReadSite{File: p, Line: 1, Kind: "template", Anchor: AnchorNone, Dir: call.Dir})
			} else if expr, err := parseTemplate(p, e.Data, lim); err != nil {
				s.sites = append(s.sites, ReadSite{File: p, Line: 1, Kind: "template", Anchor: AnchorNone, Dir: call.Dir})
			} else {
				s.funcs(expr)
			}
			if sites = append(sites, s.sites...); len(sites) > lim.readSitesCap() {
				return sites, nil
			}
		}
	}
	return sites, nil
}

// parseTemplate parses a template within the limits parseSyntax applies. A
// panic in the parser is recovered and reported as an error.
func parseTemplate(name string, src []byte, lim Limits) (expr hclsyntax.Expression, err error) {
	defer func() {
		if r := recover(); r != nil {
			expr, err = nil, errors.New("internal error while parsing")
		}
	}()
	tokens, diags := hclsyntax.LexTemplate(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, errors.New("parse error")
	}
	if len(tokens) > lim.MaxTokens || nestingDepth(tokens) > lim.MaxDepth || prefixRun(tokens) > lim.MaxPrefixOps {
		return nil, errors.New("over a parser limit")
	}
	if err := lim.chargeTokens(len(tokens)); err != nil {
		return nil, err
	}
	expr, diags = hclsyntax.ParseTemplate(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, errors.New("parse error")
	}
	return expr, nil
}

// exampleRoots lists "." and every first-level directory under examples/
// that holds a .tf file: the root modules a path from path.root or path.cwd
// may be relative to.
func exampleRoots(t *Tree) []string {
	roots := []string{"."}
	for d := range t.Dirs {
		if parts := strings.Split(d, "/"); len(parts) == 2 && parts[0] == "examples" && holdsTF(t, d) {
			roots = append(roots, d)
		}
	}
	slices.Sort(roots[1:])
	return roots
}

// holdsTF reports whether a directory holds a .tf entry directly.
func holdsTF(t *Tree, d string) bool {
	return slices.ContainsFunc(t.DirEntries(d), func(e *Entry) bool {
		name := path.Base(e.Path)
		return strings.HasSuffix(name, ".tf") && !ignoredName(name)
	})
}

// siteScan collects the read sites of one file.
type siteScan struct {
	dir, file string
	sites     []ReadSite
	held      []heldSite      // the sites of IAM path literals, kept only if they may be read as a path
	refs      map[string]bool // the aws_iam_* blocks some expression reads whole or by path; see iamRef
	uses      flowUses        // where each variable and local of the tree goes; see flowHolder
	overrides bool            // the directory holds an override file, which may change a module's source
}

// isOverride reports an override file name, whose blocks Terraform merges
// into the blocks of the same name in the other files of its directory.
func isOverride(name string) bool {
	return name == "override.tf" || strings.HasSuffix(name, "_override.tf")
}

// heldSite holds the read sites of one IAM path literal (see iamPathText)
// that is the whole value of an argument; to says where that value goes.
type heldSite struct {
	to    flowHolder
	sites []ReadSite
}

// flowNode is a variable or local of the module in dir; kind is "var" or
// "local".
type flowNode struct{ kind, dir, name string }

// flowHolder is what the whole value of an argument goes to: the path or
// path_prefix of the aws_iam_* block named block, the variable or local
// node, or, when opaque, anything, as when the value is joined, passed to a
// function or to a module whose code the tree does not hold. call marks a
// module argument: node is then the variable of the called module, which
// counts only when the tree holds that module's configuration.
type flowHolder struct {
	block  string
	node   flowNode
	call   bool
	opaque bool
}

// flowUses lists, for each variable and local, where its references go.
type flowUses map[flowNode][]flowHolder

// maxFlowSteps bounds the holders sinks visits for one literal; past it the
// literal stays a read site.
const maxFlowSteps = 256

// body scans a body; kind is the type of the block that holds it, label its
// first label, the resource or provisioner type, and name its second. top is
// set for the body of a block at the top level of a file.
func (s *siteScan) body(b *hclsyntax.Body, kind, label, name string, top bool) {
	for attr, a := range b.Attributes {
		if kind == "module" && (attr == "source" || attr == "version") {
			continue // a local source is an edge of the call graph, not a file read
		}
		if how := readAttrs[label][attr]; how != 0 && (kind == "resource" || kind == "data" || kind == "provisioner") {
			if how == readsShell {
				s.sites = append(s.sites, ReadSite{File: s.file, Line: a.Expr.Range().Start.Line, Kind: attr, Anchor: AnchorNone, Dir: s.dir})
			} else {
				s.add(attr, a.Expr, true)
			}
		}
		to := s.holder(b, kind, label, name, attr, top)
		s.flow(a.Expr, to)
		if !iamPathText(a.Expr) {
			s.expr(a.Expr)
			continue
		}
		h := siteScan{dir: s.dir, file: s.file, refs: map[string]bool{}}
		h.expr(a.Expr)
		s.held = append(s.held, heldSite{to: to, sites: h.sites})
	}
	for _, blk := range b.Blocks {
		label, name := "", ""
		if len(blk.Labels) > 0 {
			label = blk.Labels[0]
		}
		if len(blk.Labels) > 1 {
			name = blk.Labels[1]
		}
		s.body(blk.Body, blk.Type, label, name, kind == "")
	}
}

// holder returns where the whole value of argument attr of a block goes: the
// IAM path of an aws_iam_* resource or data block, a local, a variable's
// default, or the variable of a module whose source is a local path inside
// the tree. Anything else is opaque, an output value and a meta-argument
// included.
func (s *siteScan) holder(b *hclsyntax.Body, kind, label, name, attr string, top bool) flowHolder {
	switch {
	case !top:
	case (kind == "resource" || kind == "data") && strings.HasPrefix(label, "aws_iam_") && (attr == "path" || attr == "path_prefix"):
		block := label + "." + name
		if kind == "data" {
			block = "data." + block
		}
		return flowHolder{block: block}
	case kind == "locals":
		return flowHolder{node: flowNode{"local", s.dir, attr}}
	case kind == "variable" && attr == "default":
		return flowHolder{node: flowNode{"var", s.dir, label}}
	case kind == "module" && !moduleMeta[attr] && !s.overrides:
		if dir, ok := s.localSource(b); ok {
			return flowHolder{node: flowNode{"var", dir, attr}, call: true}
		}
	}
	return flowHolder{opaque: true}
}

// localSource returns the directory a module block's source names, when it
// is a literal local path. One outside the tree holds no configuration of it,
// which sinks checks.
func (s *siteScan) localSource(b *hclsyntax.Body) (string, bool) {
	a, ok := b.Attributes["source"]
	if !ok {
		return "", false
	}
	_, text, exact := staticPath(a.Expr)
	if !exact || !strings.HasPrefix(text, "./") && !strings.HasPrefix(text, "../") {
		return "", false
	}
	return path.Join(s.dir, text), true
}

// moduleMeta are the meta-arguments of a module block, which no variable of
// the called module receives.
var moduleMeta = map[string]bool{"count": true, "for_each": true, "providers": true, "depends_on": true}

// flow records in uses where each reference in e goes: to to when e is that
// reference alone, as in path = var.p, and anywhere when e does more with it.
// Only the uses of variables and locals are ever looked up; a step that is no
// attribute name, as in var["p"], keys no variable.
func (s *siteScan) flow(e hclsyntax.Expression, to flowHolder) {
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics { // the walker returns no diagnostics
		x, ok := n.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(x.Traversal) < 2 {
			return nil
		}
		a, _ := x.Traversal[1].(hcl.TraverseAttr)
		h := to
		if hclsyntax.Expression(x) != e || len(x.Traversal) != 2 {
			h = flowHolder{opaque: true}
		}
		node := flowNode{x.Traversal.RootName(), s.dir, a.Name}
		s.uses[node] = append(s.uses[node], h)
		return nil
	})
}

// sinks returns the aws_iam_* blocks whose IAM path a value that goes to h
// ends in, and false when it may also go anywhere else: an opaque holder,
// a module the tree does not hold, or more than steps holders. A value that
// a variable or local nobody reads holds goes nowhere.
func (u flowUses) sinks(t *Tree, h flowHolder, seen map[flowNode]bool, steps *int) ([]string, bool) {
	if *steps++; *steps > maxFlowSteps || h.opaque || h.call && !holdsTF(t, h.node.dir) {
		return nil, false
	}
	if h.block != "" {
		return []string{h.block}, true
	}
	if seen[h.node] {
		return nil, true
	}
	seen[h.node] = true
	var blocks []string
	for _, next := range u[h.node] {
		b, ok := u.sinks(t, next, seen, steps)
		if !ok {
			return nil, false
		}
		blocks = append(blocks, b...)
	}
	return blocks, true
}

// iamPathText reports whether e is a plain string that is an IAM path: "/"
// alone, or text of printable ASCII between a leading and a trailing "/", as
// in "/service-role/". Such a literal is no read site when it goes only to
// the IAM path of aws_iam_* blocks no expression reads by path (see sinks
// and iamRef): joined to the root module, "/" would name the whole tree. Any
// other literal, and one that may also go anywhere else, a module argument
// whose module the tree does not hold, a join separator or a variable
// default joined to path.module included, stays a read site.
func iamPathText(e hclsyntax.Expression) bool {
	anchor, text, exact := staticPath(e)
	if anchor != AnchorRoot || !exact || len(text) > maxIAMPath || !strings.HasPrefix(text, "/") || !strings.HasSuffix(text, "/") {
		return false
	}
	return !strings.ContainsFunc(text, func(r rune) bool { return r < 0x21 || r > 0x7e })
}

// maxIAMPath is the length limit of an IAM path.
const maxIAMPath = 512

// iamRef returns the address of the aws_iam_* resource or data block that t
// names, as refs and flowHolder key it, and whether t reads the block whole
// or its path or path_prefix. Index and splat steps after the address stay
// on the block, so aws_iam_role.r["k"].path and resource.aws_iam_role.r.path
// read it; an attribute other than path or path_prefix does not. A reference the traversal does not finish, as in
// aws_iam_role.r[*].path or aws_iam_role.r[each.key].arn, names the block
// whole, so it counts.
func iamRef(t hcl.Traversal) (string, bool) {
	var names []string
	want := 2
	switch root := t.RootName(); root {
	case "resource": // resource.<type>.<name> is the address <type>.<name>
	case "data":
		names, want = []string{root}, 3
	default:
		names = []string{root}
	}
	steps := t[1:]
	for len(names) < want {
		if len(steps) == 0 {
			return "", false
		}
		a, ok := steps[0].(hcl.TraverseAttr)
		if !ok {
			return "", false
		}
		names, steps = append(names, a.Name), steps[1:]
	}
	if !strings.HasPrefix(names[want-2], "aws_iam_") {
		return "", false
	}
	for _, st := range steps {
		if a, ok := st.(hcl.TraverseAttr); ok {
			return strings.Join(names, "."), a.Name == "path" || a.Name == "path_prefix"
		}
	}
	return strings.Join(names, "."), true
}

// funcs adds the read function calls inside e, a template.
func (s *siteScan) funcs(e hclsyntax.Expression) {
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics { // the walker returns no diagnostics
		if x, ok := n.(*hclsyntax.FunctionCallExpr); ok && readFuncs[x.Name] && len(x.Args) > 0 {
			s.add(x.Name, x.Args[0], true)
		}
		return nil
	})
}

// expr adds the read function calls and the path strings inside e, and
// records in refs the aws_iam_* blocks it reads.
func (s *siteScan) expr(e hclsyntax.Expression) {
	parts := map[hclsyntax.Node]bool{}                                 // literal parts of a template, judged with it
	gone := map[hclsyntax.Node]bool{}                                  // arguments whose text the call's result never holds
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics { // the walker returns no diagnostics
		switch x := n.(type) {
		case *hclsyntax.ScopeTraversalExpr:
			if block, ok := iamRef(x.Traversal); ok {
				s.refs[block] = true
			}
		case *hclsyntax.FunctionCallExpr:
			if readFuncs[x.Name] && len(x.Args) > 0 {
				s.add(x.Name, x.Args[0], true)
			}
			if i, ok := goneArg[x.Name]; ok && i < len(x.Args) {
				gone[x.Args[i]] = true
			}
		case *hclsyntax.TemplateExpr:
			for _, p := range x.Parts {
				parts[p] = true
			}
			if !gone[x] {
				s.add("path", x, false)
			}
		case *hclsyntax.TemplateWrapExpr:
			if !gone[x] {
				s.add("path", x, false)
			}
		case *hclsyntax.LiteralValueExpr:
			if !parts[x] {
				s.add("path", x, false)
			}
		}
		return nil
	})
}

// goneArg is, by built-in function, the index of the argument whose text
// the result never holds: what replace replaces, the separator split splits
// on, the characters or affix a trim removes, and what startswith, endswith
// and strcontains look for. A string there is no path, as the "/" of
// replace(var.name, "/", "-"); the other arguments stay judged, and a read
// function inside any argument stays a read site.
var goneArg = map[string]int{
	"replace": 1, "split": 0, "trim": 1, "trimprefix": 1, "trimsuffix": 1,
	"startswith": 1, "endswith": 1, "strcontains": 1,
}

// add records e as a read site. A read site, from a read function or
// attribute, whose path has no known anchor may read anything; any other
// string counts only when it is anchored or a non-empty literal.
func (s *siteScan) add(kind string, e hclsyntax.Expression, read bool) {
	anchor, text, exact := staticPath(e)
	switch {
	case anchor == AnchorNone && !read:
		return
	case !read && anchor == AnchorRoot && exact && path.Clean(text) == ".":
		return // an empty or "." string, as in a default of "", reads nothing by itself
	}
	s.sites = append(s.sites, ReadSite{File: s.file, Line: e.Range().Start.Line, Kind: kind, Anchor: anchor,
		Dir: s.dir, Text: text, Exact: exact})
}

// staticPath returns the anchor of the path e is, the static text after it
// up to its first dynamic part, and whether there is no dynamic part.
func staticPath(e hclsyntax.Expression) (anchor, text string, exact bool) {
	switch x := e.(type) {
	case *hclsyntax.TemplateWrapExpr:
		return staticPath(x.Wrapped)
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String && x.Val.IsKnown() && !x.Val.IsNull() {
			return AnchorRoot, x.Val.AsString(), true
		}
	case *hclsyntax.ScopeTraversalExpr:
		if a := pathAnchor(x); a != "" {
			return a, "", false // a directory: whatever is under it
		}
	case *hclsyntax.TemplateExpr:
		var b strings.Builder
		anchor = AnchorRoot
		for i, p := range x.Parts {
			if lit, ok := p.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
				b.WriteString(lit.Val.AsString())
				continue
			}
			if tr, ok := p.(*hclsyntax.ScopeTraversalExpr); ok && i == 0 && pathAnchor(tr) != "" {
				anchor = pathAnchor(tr)
				continue
			}
			if i == 0 {
				return AnchorNone, "", false
			}
			return anchor, b.String(), false
		}
		return anchor, b.String(), true
	}
	return AnchorNone, "", false
}

// pathAnchor returns the anchor of path.module, path.root or path.cwd, or "".
func pathAnchor(x *hclsyntax.ScopeTraversalExpr) string {
	if len(x.Traversal) != 2 || x.Traversal.RootName() != "path" {
		return ""
	}
	attr, ok := x.Traversal[1].(hcl.TraverseAttr)
	if !ok {
		return ""
	}
	switch attr.Name {
	case "module":
		return AnchorModule
	case "root", "cwd":
		return AnchorRoot
	}
	return ""
}

// Reaches reports whether the site may read p. roots are the directories a
// root-anchored path may be relative to.
func (s ReadSite) Reaches(p string, roots []string) bool {
	bases := roots
	switch s.Anchor {
	case AnchorNone:
		return true
	case AnchorModule:
		bases = []string{s.Dir}
	}
	for _, base := range bases {
		full := path.Join(base, s.Text)
		switch {
		case full == ".." || strings.HasPrefix(full, "../"):
			return true // it leaves the tree and may come back in by any path
		case full == "." || p == full || strings.HasPrefix(p, full+"/"):
			return true
		case !s.Exact && strings.HasPrefix(p, full):
			return true // a dynamic part may finish the name
		}
	}
	return false
}

// ReachesDir reports whether the site may read p or any path below it, for a
// p that stands for a directory whose files the tree does not hold, such as a
// gitlink.
func (s ReadSite) ReachesDir(p string, roots []string) bool {
	bases := roots
	if s.Anchor == AnchorModule {
		bases = []string{s.Dir}
	}
	for _, base := range bases {
		if strings.HasPrefix(path.Join(base, s.Text), p+"/") {
			return true
		}
	}
	return s.Reaches(p, roots)
}

// ReachedBy reports whether any of sites may read p.
func ReachedBy(p string, sites []ReadSite, roots []string) bool {
	for _, s := range sites {
		if s.Reaches(p, roots) {
			return true
		}
	}
	return false
}
