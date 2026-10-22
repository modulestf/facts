package facts

import (
	"bytes"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// scopeConfigRules is the scope_config rule family: the scope type rules the
// engine reads from the parsed configuration. Each one follows the Fails
// when text of the reviewer's Check F and the registry row of its id, and
// reads the base, so each sets NeedsBase. Every rule here is a candidate.
//
// Known gaps, each an unknown the model still owns: a directory more than
// one level under modules/ is not read, as for every rule (the deep-nesting
// oracle), and a test helper directory under tests/ is not read, since such
// paths reach no area (bead mtf-lcc.2.51).
var scopeConfigRules = []Rule{
	{ID: "scope.module-call", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot, AreaSubmodule}, NeedsBase: true, Check: checkScopeModuleCall},
	{ID: "scope.new-submodule", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaSubmodule}, NeedsBase: true, Check: checkNewSubmodule},
	{ID: "scope.provider", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule}, CrossArea: true, NeedsBase: true, Check: checkScopeProvider},
}

// scopeRecordFile is the module's exceptions file, read from the base only.
const scopeRecordFile = "docs/SCOPE.md"

const defaultRegistryHost = "registry.terraform.io"

// boundaryArea reports whether an area is a boundary directory: the root or
// a submodule.
func boundaryArea(a *Area) bool { return a.Kind == AreaRoot || a.Kind == AreaSubmodule }

// newModuleBase reports whether the base has no boundary directory: no .tf
// or .tf.json file at the root or under modules/. That is a new module,
// whose one decision, scope.new-module, replaces scope.provider and
// scope.new-submodule.
func newModuleBase(base *Tree) bool {
	return !slices.ContainsFunc(base.Paths, func(p string) bool {
		d := path.Dir(p)
		return (d == "." || strings.HasPrefix(d, "modules/")) && (strings.HasSuffix(p, ".tf") || strings.HasSuffix(p, ".tf.json"))
	})
}

// scopeRecord reads the Allowed types and Allowed providers of the base
// docs/SCOPE.md. An entry is a list item whose first element is a code span.
// An entry this reader misses only makes more candidates. A base with no
// such file has no exceptions; one that is not a regular file is bad.
func scopeRecord(base *Tree) (types, providers map[string]bool, bad *Result) {
	types, providers = map[string]bool{}, map[string]bool{}
	data, _, bad := fileAt(base, scopeRecordFile)
	if bad != nil {
		return nil, nil, bad
	}
	section := ""
	for _, l := range splitLines(data) {
		s := strings.TrimRight(string(l), " \r")
		if strings.HasPrefix(s, "#") {
			section = s
			continue
		}
		m := scopeEntry.FindStringSubmatch(s)
		switch {
		case m == nil:
		case section == "## Allowed types":
			types[m[1]] = true
		case section == "## Allowed providers":
			providers[canonicalProvider(m[1])] = true
		}
	}
	return types, providers, nil
}

var scopeEntry = regexp.MustCompile("^[-*+]\\s+`([^`]+)`")

// canonicalProvider is a provider source as <host>/<namespace>/<type>,
// lowercased, with the registry host by default and hashicorp for a bare name.
func canonicalProvider(src string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(src)), "/")
	switch len(parts) {
	case 1:
		parts = []string{defaultRegistryHost, "hashicorp", parts[0]}
	case 2:
		parts = append([]string{defaultRegistryHost}, parts...)
	}
	return strings.Join(parts, "/")
}

// providerSite is one place a module names a provider: a required_providers
// entry, a provider block, or a resource or data block.
type providerSite struct {
	src    string // canonical source, or "?<text>" when it cannot be read
	file   string // relative to the module root
	line   int    // the site's own line
	evLine int    // the line evidence cites: a required_providers source line
	block  string // address of the top-level block holding the site
}

// providerSites returns every provider site of m, a module of the area at
// dir. A local name resolves through m's required_providers, and to
// hashicorp/<name> otherwise; the built-in terraform provider is skipped.
func providerSites(m *Module, dir string) []providerSite {
	local := map[string]string{}
	var out []providerSite
	for _, t := range m.Terraform {
		for _, rb := range t.Body.Blocks {
			if rb.Type != "required_providers" {
				continue
			}
			for _, at := range rb.Body.Attributes {
				src, ev := requiredSource(at)
				local[at.Name] = src
				out = append(out, providerSite{src: src, file: areaFile(dir, t.File), line: at.Range.Start.Line, evLine: ev, block: "terraform"})
			}
		}
	}
	resolve := func(name string) (string, bool) {
		if s, ok := local[name]; ok {
			return s, true
		}
		return canonicalProvider(name), name != "terraform"
	}
	add := func(name, file string, line int, block string) {
		if s, ok := resolve(name); ok {
			out = append(out, providerSite{src: s, file: areaFile(dir, file), line: line, evLine: line, block: block})
		}
	}
	// blockLocal is the local name of a block's provider: its provider
	// argument up to the first dot, or its type up to the first underscore.
	blockLocal := func(typ string, b Body) string {
		if e := b.Arg("provider"); e != nil {
			return strings.TrimSpace(strings.SplitN(e.Text, ".", 2)[0])
		}
		return strings.SplitN(typ, "_", 2)[0]
	}
	for _, p := range m.Providers {
		addr := "provider." + p.Name
		if alias, ok := literalAlias(p.Body); ok {
			addr += "." + alias
		}
		add(p.Name, p.File, p.Line, addr)
	}
	for _, r := range m.Resources {
		addr := r.Type + "." + r.Name
		if r.Mode == "data" {
			addr = "data." + addr
		}
		add(blockLocal(r.Type, r.Body), r.File, r.Line, addr)
	}
	for _, o := range m.Other {
		if o.Type != "check" {
			continue
		}
		for _, nb := range o.Body.Blocks {
			if nb.Type == "data" && len(nb.Labels) > 0 {
				add(blockLocal(nb.Labels[0], nb.Body), o.File, nb.Range.Start.Line, strings.Join(append([]string{o.Type}, o.Labels...), "."))
			}
		}
	}
	return out
}

// requiredSource returns the canonical source of a required_providers entry
// and the line of its source key, or of the entry when it has none. A source
// that is not a plain string cannot be read.
func requiredSource(at Attribute) (src string, line int) {
	line = at.Range.Start.Line
	src = canonicalProvider(at.Name)
	e, diags := hclsyntax.ParseExpression([]byte(at.Value.Text), at.Value.Range.Filename, at.Value.Range.Start)
	obj, ok := e.(*hclsyntax.ObjectConsExpr)
	if diags.HasErrors() || !ok {
		return src, line // the legacy string form is a version constraint
	}
	for _, it := range obj.Items {
		if keyName(it.KeyExpr) != "source" {
			continue
		}
		line = it.ValueExpr.Range().Start.Line
		if s, ok := literalString(it.ValueExpr); ok {
			return canonicalProvider(s), line
		}
		return "?" + at.Value.Text, line
	}
	return src, line
}

// changedFiles caches the changed head lines of files against the base.
type changedFiles struct {
	head, base *Tree
	budget     int
	lines      map[string][]lineRange
	over       bool // a file passed the diff bounds
}

func (c *changedFiles) changed(file string, line int) bool {
	r, ok := c.lines[file]
	if !ok {
		h, _, _ := fileAt(c.head, file)
		b, _, _ := fileAt(c.base, file)
		var fine bool
		if r, fine = changedLines(b, h, &c.budget); !fine {
			c.over = true
		}
		c.lines[file] = r
	}
	return inRanges(r, line)
}

// scope.provider: a head provider, in canonical form, that is not a base
// provider. Base providers are the union over every boundary directory at
// base of its required_providers sources and the providers of its resource
// and data blocks, plus the Allowed providers of the base docs/SCOPE.md.
// One candidate per provider, at its first site in file order that the
// change adds, in the area of that site; its evidence is every site of the
// provider on a changed line. A new module's empty base is not this rule.
//
// A provider whose every changed site lies in a block the base already has,
// such as an entry added to an existing required_providers, has no line the
// newly-introduced scope keeps, so the pass is not evaluated rather than
// clean, and its other candidates are dropped with it.
func checkScopeProvider(a *Area) Result {
	const id = "scope.provider"
	if newModuleBase(a.baseTree) {
		return Result{}
	}
	_, allowed, bad := scopeRecord(a.baseTree)
	if bad != nil {
		return *bad
	}
	areas := make([]*Area, 0, len(a.peers))
	for _, p := range a.peers {
		if boundaryArea(p) {
			areas = append(areas, p)
		}
	}
	slices.SortFunc(areas, func(x, y *Area) int { return strings.Compare(x.Path, y.Path) })
	baseSet := allowed
	for _, p := range areas {
		if p.Base != nil {
			for _, s := range providerSites(p.Base, p.Path) {
				// A provider block alone makes no base provider: module-scope.md
				// Providers counts required_providers and resource and data blocks.
				if !strings.HasPrefix(s.block, "provider.") {
					baseSet[s.src] = true
				}
			}
		}
	}
	byProvider := map[string][]providerSite{}
	var order []string
	ch := &changedFiles{head: a.headTree, base: a.baseTree, budget: maxDiffTotalCells, lines: map[string][]lineRange{}}
	added := map[string]bool{} // "<file> <address>" of each head block the base lacks
	for _, p := range areas {
		baseAddrs := map[string]bool{}
		for _, b := range topBlocks(p.Base) {
			baseAddrs[b.addr] = true
		}
		for _, s := range providerSites(p.Head, p.Path) {
			if baseSet[s.src] {
				continue
			}
			if !baseAddrs[s.block] {
				added[s.file+" "+s.block] = true
			}
			if byProvider[s.src] == nil {
				order = append(order, s.src)
			}
			byProvider[s.src] = append(byProvider[s.src], s)
		}
	}
	var res Result
	for _, src := range order {
		sites := byProvider[src]
		slices.SortFunc(sites, func(x, y providerSite) int {
			if c := strings.Compare(x.file, y.file); c != 0 {
				return c
			}
			return x.line - y.line
		})
		ev := newPathEvidence()
		var anchor *providerSite
		for i, s := range sites {
			if !ch.changed(s.file, s.line) && !ch.changed(s.file, s.evLine) {
				continue
			}
			ev.line("head", s.file, s.evLine, lineOf(a.headTree.Entries[s.file].Data, s.evLine))
			if anchor == nil && added[s.file+" "+s.block] && ch.changed(s.file, s.line) {
				anchor = &sites[i]
			}
		}
		if ch.over {
			return Result{NotEvaluated: "a changed file is past the line diff limit", Code: ReasonLimitExceeded}
		}
		owner := areaOf(sites[0].file)
		if anchor != nil {
			owner = areaOf(anchor.file)
		}
		switch {
		case owner != a.Path || len(ev.ev.SourceLines) == 0:
			// Another area reports it, or no site is in the change.
		case anchor == nil:
			// Reached only for a new provider with a changed site and no added
			// block to anchor it. The whole pass is then not evaluated, so the
			// candidates of every other new provider in the run are dropped too.
			// shortcut: registry scope newly-introduced drops entries added to existing blocks; flips to touched-expression with the registry pin bump (mtf-lcc.2.40)
			return Result{NotEvaluated: "provider " + short(src) + " is added only inside a block the base already has", Code: ReasonUnsupportedFile}
		default:
			ev.ev.Head = ev.value(strings.TrimPrefix(src, defaultRegistryHost+"/"))
			line := anchor.line
			res.Candidates = append(res.Candidates, Candidate{RuleID: id, File: anchor.file, Line: &line,
				Subject: subject(owner, `provider["`+short(src)+`"]`), Evidence: ev.ev, EvidenceComplete: ev.complete})
		}
	}
	return res
}

// areaOf is the area of a file path in a boundary directory.
func areaOf(file string) string {
	a, _, _ := Classify(file)
	return a
}

// scope.module-call: a module block the change adds, or whose source it
// edits, whose source is not a local path that stays inside the module:
// beginning with ./ or ../, with no //, resolving lexically to a directory
// under the top-level modules/ other than the calling one, which the head
// has, with no symbolic link on the way. A call whose source the base
// already had is not an addition. Its evidence is the source line, with the
// source as written at head and at base.
func checkScopeModuleCall(a *Area) Result {
	const id = "scope.module-call"
	baseSrc := map[string]string{}
	for _, c := range a.Base.ModuleCalls {
		baseSrc[c.Name] = sourceText(a.baseTree, a.Path, c)
	}
	var res Result
	for _, c := range a.Head.ModuleCalls {
		head := sourceText(a.headTree, a.Path, c)
		if b, ok := baseSrc[c.Name]; (ok && b == head) || localModuleSource(a, c.Source) {
			continue
		}
		ev := newPathEvidence()
		file := areaFile(a.Path, c.File)
		if head != "" {
			n := c.SourceRange.Start.Line
			ev.line("head", file, n, lineOf(a.headTree.Entries[file].Data, n))
			ev.ev.Head = ev.value(head)
		}
		if b, ok := baseSrc[c.Name]; ok && b != "" {
			ev.ev.Base = ev.value(b)
		}
		line := c.Line
		res.Candidates = append(res.Candidates, Candidate{RuleID: id, File: file, Line: &line,
			Subject: subject(a.Path, "module."+short(c.Name)), Evidence: ev.ev, EvidenceComplete: ev.complete})
	}
	return res
}

// sourceText is a module call's source expression as written, or "".
func sourceText(t *Tree, dir string, c ModuleCall) string {
	data, _, _ := fileAt(t, areaFile(dir, c.File))
	if c.SourceRange.Empty() || c.SourceRange.End.Byte > len(data) {
		return ""
	}
	return string(bytes.TrimSpace(c.SourceRange.SliceBytes(data)))
}

func localModuleSource(a *Area, src string) bool {
	if (!strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../")) || strings.Contains(src, "//") {
		return false
	}
	p := path.Join(a.Path, src)
	if !strings.HasPrefix(p, "modules/") || p == a.Path || !a.headTree.Dirs[p] {
		return false
	}
	for q := p; q != "."; q = path.Dir(q) {
		if e := a.headTree.Entries[q]; e != nil && e.Kind == KindSymlink {
			return false
		}
	}
	return true
}

// scope.new-submodule: a directory under modules/ that the base lacks, with
// at least one managed type that is neither an adjunct of the profile nor
// allowed by the base docs/SCOPE.md. One candidate per directory, with no
// line; its evidence is each such type and its first block. A directory
// renamed whole from one the base has is still read as new: the trees carry
// no rename, so the confirmer settles it. A new module's empty base is not
// this rule.
func checkNewSubmodule(a *Area) Result {
	const id = "scope.new-submodule"
	if a.BaseExists || !a.HeadExists || newModuleBase(a.baseTree) {
		return Result{}
	}
	if res, ok := needProfile(a); !ok {
		return res
	}
	allowed, _, bad := scopeRecord(a.baseTree)
	if bad != nil {
		return *bad
	}
	ev := newPathEvidence()
	var types []string
	for _, r := range a.Head.Resources {
		if r.Mode != "resource" || slices.Contains(a.Profile.AdjunctTypes, r.Type) || allowed[r.Type] || slices.Contains(types, r.Type) {
			continue
		}
		types = append(types, r.Type)
		file := areaFile(a.Path, r.File)
		ev.line("head", file, r.Line, lineOf(a.headTree.Entries[file].Data, r.Line))
	}
	if len(types) == 0 {
		return Result{}
	}
	ev.ev.Head = ev.value(strings.Join(types, ", "))
	return Result{Candidates: []Candidate{ev.candidate(id, a.Path, nil)}}
}
