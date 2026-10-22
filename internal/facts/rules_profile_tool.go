package facts

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// profileToolRules is the profile_tool rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
var profileToolRules = []Rule{
	{ID: "profile.nonstandard-config-file", Tier: "tool", Class: ClassPath, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkNonstandardConfigFile},
	{ID: "profile.putin-khuylo-missing", Tier: "tool", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot}, NeedsBase: true, Check: checkPutinKhuyloMissing},
	{ID: "profile.required-file-missing", Tier: "tool", Class: ClassPath, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, NeedsBase: true, Check: checkRequiredFileMissing},
}

const moduleLayoutRef = "Module layout in the profile's PROFILE.md"

// newArea reports whether the change adds the area: the base has nothing at
// or under its path. For the root that is an empty base, a new module. It
// needs a base tree.
func newArea(a *Area) bool {
	if a.Path == rootArea {
		return len(a.baseTree.Paths) == 0
	}
	for _, p := range a.baseTree.Paths {
		if p == a.Path || strings.HasPrefix(p, a.Path+"/") {
			return false
		}
	}
	return true
}

// inChange reports whether the change adds or edits the head entry e.
func inChange(a *Area, e *Entry) bool {
	return a.baseTree == nil || !sameEntry(e, a.baseTree.Entries[e.Path])
}

// profile.required-file-missing: an area the change adds lacks a path the
// profile's required-files block names for its kind. A new root also needs
// every template destination and the example directories. Any entry at the
// path counts as present, a link included, and a missing directory is one
// finding. An area that exists at the base is held to nothing: it is no new
// module, and a file it never had is not this change's finding.
func checkRequiredFileMissing(a *Area) Result {
	if res, ok := needProfile(a); !ok {
		return res
	}
	if !a.HeadExists || !newArea(a) {
		return Result{}
	}
	rf := a.Profile.RequiredFiles
	var want []string
	switch a.Kind {
	case AreaRoot:
		want = slices.Clone(rf.Root)
		for _, t := range a.Profile.Templates {
			want = append(want, t.Destination)
		}
		want = append(want, rf.ExampleDirs...)
	case AreaSubmodule:
		want = rf.Submodule
	case AreaExample:
		want = rf.Example
	}
	const id = "profile.required-file-missing"
	var out []Finding
	seen := map[string]bool{}
	for _, w := range want {
		p := path.Join(a.Path, w)
		if seen[p] || a.headTree.Entries[p] != nil || a.headTree.Dirs[p] {
			continue
		}
		seen[p] = true
		out = append(out, Finding{RuleID: id, Severity: "MEDIUM", File: p,
			Summary:      fmt.Sprintf("The new module has no %s, a path the profile requires for a %s.", p, a.Kind),
			SuggestedFix: fmt.Sprintf("Add `%s` from the template the templates map names for it, or, for a missing example or submodule directory, the directory with its files, per %s.", p, moduleLayoutRef),
		})
	}
	return Result{Findings: out}
}

// nonstandardName reports a file name the profile's quality gate cannot
// read: JSON syntax, an override file or an OpenTofu file.
func nonstandardName(name string) bool {
	n := strings.TrimSuffix(name, ".json")
	return strings.HasSuffix(name, ".tf.json") || n == "override.tf" || strings.HasSuffix(n, "_override.tf") ||
		strings.HasSuffix(name, ".tofu")
}

// profile.nonstandard-config-file: a file the change adds or edits is
// *.tf.json, an override file or a .tofu file, or a .tf file holds a block
// only OpenTofu has, an encryption block in the terraform block or for_each
// on a provider block. The block case reads every byte through the HCL
// lexer, so it is exhaustive even where the file does not parse. A .tf file
// in the change whose bytes are not read, a link included, leaves it
// undecided. Every required_version is read as admitting Terraform: telling a
// constraint that only OpenTofu releases meet needs a release list.
func checkNonstandardConfigFile(a *Area) Result {
	const id = "profile.nonstandard-config-file"
	fix := "Rewrite a `*.tf.json` file as a `.tf` file with the same blocks, or fold an override file into the file it overrides and delete it, or rewrite a `.tofu` file or an OpenTofu-only block in Terraform's language, per Configuration file formats in the profile's code-conventions.md."
	if res, ok := needProfile(a); !ok {
		return res
	}
	if !a.HeadExists {
		return Result{}
	}
	var out []Finding
	for _, e := range a.headTree.DirEntries(a.Path) {
		name := path.Base(e.Path)
		switch {
		case ignoredName(name):
		case nonstandardName(name):
			out = append(out, Finding{RuleID: id, Severity: "HIGH", File: e.Path,
				Summary:      fmt.Sprintf("%s is a configuration file format the profile's quality gate cannot read.", e.Path),
				SuggestedFix: fix})
		case !strings.HasSuffix(name, ".tf"):
		case e.Kind != KindFile:
			if inChange(a, e) {
				return Result{NotEvaluated: fmt.Sprintf("%s is %s, so its blocks are not read", e.Path, kindText(e.Kind)), Code: kindCode(e.Kind)}
			}
		default:
			for _, line := range tofuOnlyLines(e.Data) {
				l := line
				out = append(out, Finding{RuleID: id, Severity: "HIGH", File: e.Path, Line: &l,
					Summary:      fmt.Sprintf("%s line %d is a block only OpenTofu has, which Terraform and the profile's quality gate cannot read.", e.Path, l),
					SuggestedFix: fix})
			}
		}
	}
	return Result{Findings: out}
}

// tofuOnlyLines returns the lines of the OpenTofu-only blocks of a .tf file:
// an encryption block directly in a terraform block, and a for_each argument
// directly in a provider block. It follows the token stream, so a file that
// does not parse is still read in full.
func tofuOnlyLines(src []byte) []int {
	toks, _ := hclsyntax.LexConfig(src, "", hcl.InitialPos)
	var (
		stack []string // the statement head of each open brace
		head  string   // first name of the current statement
		lines []int
	)
	start := true
	for i, t := range toks {
		switch t.Type {
		case hclsyntax.TokenNewline:
			start = true
			continue
		case hclsyntax.TokenOBrace:
			if head == "encryption" && toks[i-1].Type == hclsyntax.TokenIdent && slices.Equal(stack, []string{"terraform"}) {
				lines = append(lines, t.Range.Start.Line)
			}
			stack, head, start = append(stack, head), "", true
			continue
		case hclsyntax.TokenCBrace:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			head = ""
		case hclsyntax.TokenIdent:
			if start {
				head = string(t.Bytes)
			}
		case hclsyntax.TokenEqual:
			if head == "for_each" && toks[i-1].Type == hclsyntax.TokenIdent && slices.Equal(stack, []string{"provider"}) {
				lines = append(lines, t.Range.Start.Line)
			}
		}
		start = false
	}
	return lines
}

// gateNode is what one expression, a count, a for_each or a local, reads on
// its way to a creation input.
type gateNode struct {
	reaches bool     // some path reaches a creation input
	open    bool     // some path reaches one without reading var.putin_khuylo
	via     []string // edited locals on a path that reaches one
	openVia []string // edited locals on an open path
}

// gateGraph answers gateNode for the expressions of a root module.
type gateGraph struct {
	creation map[string]bool     // creation inputs, as var.<name>
	refs     map[string][]string // references of each head local, by name
	edited   map[string]bool     // locals the change adds or edits
	memo     map[string]*gateNode
}

func exprRefs(e *Expr) []string {
	if e == nil {
		return nil
	}
	// The text came from a parse that succeeded, so it parses again.
	x, diags := hclsyntax.ParseExpression([]byte(e.Text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	refs, _ := references(x)
	return refs
}

func newGateGraph(a *Area) *gateGraph {
	g := &gateGraph{creation: map[string]bool{}, refs: map[string][]string{}, edited: map[string]bool{}, memo: map[string]*gateNode{}}
	base := map[string]string{}
	for _, l := range a.Base.Locals {
		base[l.Name] = l.Value.Text
	}
	for _, l := range a.Head.Locals {
		r := exprRefs(&l.Value)
		g.refs[l.Name] = r
		if t, ok := base[l.Name]; !ok || t != l.Value.Text {
			g.edited[l.Name] = true
		}
		if slices.Contains(r, "var.putin_khuylo") {
			for _, v := range r {
				if strings.HasPrefix(v, "var.") && v != "var.putin_khuylo" {
					g.creation[v] = true
				}
			}
		}
	}
	if len(g.creation) == 0 {
		g.creation["var.create"] = true
	}
	return g
}

// node walks refs. A node that reads var.putin_khuylo itself gates every
// path through it. A local met again on the current walk adds nothing.
func (g *gateGraph) node(refs []string, walking map[string]bool) gateNode {
	var n gateNode
	gated := slices.Contains(refs, "var.putin_khuylo")
	for _, r := range refs {
		if g.creation[r] {
			n.reaches, n.open = true, !gated
			continue
		}
		name, ok := strings.CutPrefix(r, "local.")
		if !ok || walking[name] {
			continue
		}
		l := g.local(name, walking)
		if !l.reaches {
			continue
		}
		n.reaches = true
		n.via = append(n.via, l.via...)
		if g.edited[name] {
			n.via = append(n.via, name)
		}
		if l.open && !gated {
			n.open = true
			n.openVia = append(n.openVia, l.openVia...)
			if g.edited[name] {
				n.openVia = append(n.openVia, name)
			}
		}
	}
	return n
}

func (g *gateGraph) local(name string, walking map[string]bool) gateNode {
	if n, ok := g.memo[name]; ok {
		return *n
	}
	if _, ok := g.refs[name]; !ok {
		return gateNode{}
	}
	walking[name] = true
	n := g.node(g.refs[name], walking)
	delete(walking, name)
	g.memo[name] = &n
	return n
}

// judgedBlock is a resource or module block of the root with its count or
// for_each, and that expression at the base, nil when the block or the
// argument is absent there.
type judgedBlock struct {
	file string
	expr *Expr
	base *Expr
	head bool // the block is absent at the base
}

func rootBlocks(a *Area) []judgedBlock {
	meta := func(b Body) *Expr {
		if e := b.Arg("count"); e != nil {
			return e
		}
		return b.Arg("for_each")
	}
	base := map[string]*Expr{}
	atBase := map[string]bool{}
	for _, r := range a.Base.Resources {
		if r.Mode == "resource" {
			k := r.Type + "." + r.Name
			atBase[k], base[k] = true, meta(r.Body)
		}
	}
	for _, m := range a.Base.ModuleCalls {
		k := "module." + m.Name
		atBase[k], base[k] = true, meta(m.Body)
	}
	var out []judgedBlock
	add := func(k, file string, b Body) {
		if e := meta(b); e != nil {
			out = append(out, judgedBlock{file: file, expr: e, base: base[k], head: !atBase[k]})
		}
	}
	for _, r := range a.Head.Resources {
		if r.Mode == "resource" {
			add(r.Type+"."+r.Name, r.File, r.Body)
		}
	}
	for _, m := range a.Head.ModuleCalls {
		add("module."+m.Name, m.File, m.Body)
	}
	return out
}

// profile.putin-khuylo-missing: a root resource or module block whose count
// or for_each reads a creation input on a path that skips var.putin_khuylo.
// For a root the change adds, the whole root is judged and one finding goes
// on variables.tf. For a root on the base, only the blocks the change adds,
// those whose count or for_each it edits and those that reach a creation
// input through a local it edits are judged; a gate lost in an edited local
// is one finding at that local.
func checkPutinKhuyloMissing(a *Area) Result {
	const id = "profile.putin-khuylo-missing"
	if res, ok := needProfile(a); !ok {
		return res
	}
	if !a.HeadExists {
		return Result{}
	}
	g := newGateGraph(a)
	blocks := rootBlocks(a)
	fix := "In the root module only: declare `variable \"putin_khuylo\"` after `variable \"create\"` as `templates/variables.tf.template` does, and gate each named block on `var.create && var.putin_khuylo`, through the creation local `templates/main.tf.template` defines, per " + moduleLayoutRef + "."
	if newArea(a) {
		declared, open := false, false
		var first *Variable
		for i, v := range a.Head.Variables {
			if v.File != "variables.tf" {
				continue
			}
			declared = declared || v.Name == "putin_khuylo"
			if g.creation["var."+v.Name] && (first == nil || v.Line < first.Line) {
				first = &a.Head.Variables[i]
			}
		}
		for _, b := range blocks {
			open = open || g.node(exprRefs(b.expr), map[string]bool{}).open
		}
		if declared && !open {
			return Result{}
		}
		f := Finding{RuleID: id, Severity: "MEDIUM", File: areaFile(a.Path, "variables.tf"),
			Summary:      "The new root module creates resources without the creation gate the profile requires: no `putin_khuylo` input, or a block whose count or for_each skips it.",
			SuggestedFix: fix}
		if first != nil {
			l := first.Line
			f.Line = &l
		}
		return Result{Findings: []Finding{f}}
	}
	type spot struct {
		file string
		line int
	}
	at := map[spot]bool{}
	var out []Finding
	locals := map[string]Local{}
	for _, l := range a.Head.Locals {
		locals[l.Name] = l
	}
	for _, b := range blocks {
		n := g.node(exprRefs(b.expr), map[string]bool{})
		edited := b.head || b.base == nil || b.base.Text != b.expr.Text
		if !n.open || (!edited && len(n.via) == 0) {
			continue
		}
		s := spot{b.file, b.expr.Range.Start.Line}
		what := "this block's count or for_each"
		if len(n.openVia) > 0 {
			// The first edited local, by position, takes every block whose
			// gate it loses.
			l := locals[slices.MinFunc(n.openVia, func(x, y string) int {
				lx, ly := locals[x], locals[y]
				if c := strings.Compare(lx.File, ly.File); c != 0 {
					return c
				}
				return lx.Line - ly.Line
			})]
			s, what = spot{l.File, l.Line}, fmt.Sprintf("local %q", short(l.Name))
		}
		if at[s] {
			continue
		}
		at[s] = true
		out = append(out, finding(id, a.Path, s.file, s.line,
			fmt.Sprintf("In %s, %s reads a creation input without `var.putin_khuylo`, so the module creates resources without the profile's creation gate.", areaFile(a.Path, s.file), what),
			fix))
	}
	return Result{Findings: out}
}
