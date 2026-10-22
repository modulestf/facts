package facts

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
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
		strings.HasSuffix(n, ".tofu")
}

// profile.nonstandard-config-file: a file the change adds or edits is
// *.tf.json, an override file or a .tofu or .tofu.json file, or a .tf file holds a block
// only OpenTofu has, an encryption block in the terraform block or for_each
// on a provider block. The block case reads every byte through the HCL
// lexer, so it is exhaustive even where the file does not parse. A .tf file
// in the change whose bytes are not read, a link included, or that the lexer
// cannot read, leaves it undecided. Such an area loses its file-name findings
// too: a result is either not evaluated or has findings. Every required_version is read as admitting Terraform: telling a
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
			lines, ok := tofuOnlyLines(e.Data)
			if !ok && inChange(a, e) {
				return Result{NotEvaluated: e.Path + " cannot be lexed, so its blocks are not read", Code: ReasonParseError}
			}
			for _, line := range lines {
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
// does not parse is still read in full. ok is false when the lexer reports
// an error, such as an unterminated string, and the lines are then unknown.
func tofuOnlyLines(src []byte) (lines []int, ok bool) {
	toks, diags := hclsyntax.LexConfig(src, "", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	var (
		stack []string // the statement head of each open brace
		head  string   // first name of the current statement
	)
	start := true
	for i, t := range toks {
		switch t.Type {
		case hclsyntax.TokenNewline:
			start = true
			continue
		case hclsyntax.TokenComment:
			// A line comment carries the newline that ends it; a /* */ comment
			// is skipped like space.
			start = start || strings.HasSuffix(string(t.Bytes), "\n")
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
	return lines, true
}

// gateNode is what one expression, a count, a for_each or a local, reads on
// its way to a creation input.
type gateNode struct {
	reaches bool     // it reads a creation input, directly or through locals
	open    bool     // it reaches one and the gate does not decide it
	via     []string // edited locals on a path that reaches one
	lostVia []string // edited locals that reach one and are not gated
}

// gateGraph answers gateNode for the expressions of a root module.
type gateGraph struct {
	creation map[string]bool                 // creation inputs, as var.<name>
	refs     map[string][]string             // references of each head local, by name
	exprs    map[string]hclsyntax.Expression // each head local's value
	edited   map[string]bool                 // locals the change adds or edits
	memo     map[string]*gateNode
	guarded  map[string]bool // memo of gates, per local
	walking  map[string]bool // locals on the current walk
}

// parseExpr parses an expression's text again, nil for none. The text came
// from a parse that succeeded, so it parses.
func parseExpr(e *Expr) hclsyntax.Expression {
	if e == nil {
		return nil
	}
	x, diags := hclsyntax.ParseExpression([]byte(e.Text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	return x
}

func exprRefs(e *Expr) []string {
	x := parseExpr(e)
	if x == nil {
		return nil
	}
	refs, _ := references(x)
	return refs
}

// createsNothing reports an expression that gives a count of 0 or an empty
// for_each: 0, null, [], {}, or toset, tolist or tomap of one of those.
func createsNothing(e hclsyntax.Expression) bool {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return createsNothing(x.Expression)
	case *hclsyntax.LiteralValueExpr:
		v := x.Val
		return v.IsNull() || (v.Type() == cty.Number && v.IsKnown() && v.Equals(cty.Zero).True())
	case *hclsyntax.TupleConsExpr:
		return len(x.Exprs) == 0
	case *hclsyntax.ObjectConsExpr:
		return len(x.Items) == 0
	case *hclsyntax.FunctionCallExpr:
		return (x.Name == "toset" || x.Name == "tolist" || x.Name == "tomap") && len(x.Args) == 1 && createsNothing(x.Args[0])
	}
	return false
}

// guards reports whether the gate decides an expression, read from its
// syntax: the expression is var.putin_khuylo or a local the gate decides, an
// && chain with such an operand, a conditional whose condition is gated, or
// whose true branch is, and whose false branch creates nothing, or a for
// expression whose filter is gated. The gate under || or !, in a branch
// beside one that creates something, or only in a function argument does
// not decide it.
func (g *gateGraph) guards(e hclsyntax.Expression) bool {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return g.guards(x.Expression)
	case *hclsyntax.ScopeTraversalExpr:
		addr := refAddress(x.Traversal)
		if addr == "var.putin_khuylo" {
			return true
		}
		if name, ok := strings.CutPrefix(addr, "local."); ok {
			return g.localGuards(name)
		}
	case *hclsyntax.BinaryOpExpr:
		return x.Op == hclsyntax.OpLogicalAnd && (g.guards(x.LHS) || g.guards(x.RHS))
	case *hclsyntax.ConditionalExpr:
		return createsNothing(x.FalseResult) && (g.guards(x.Condition) || g.guards(x.TrueResult))
	case *hclsyntax.ForExpr:
		return x.CondExpr != nil && g.guards(x.CondExpr)
	}
	return false
}

// localGuards is guards for a head local. A local met again on the current
// walk is not gated by that path.
func (g *gateGraph) localGuards(name string) bool {
	if v, ok := g.guarded[name]; ok {
		return v
	}
	x, ok := g.exprs[name]
	if !ok || x == nil || g.walking[name] {
		return false
	}
	g.walking[name] = true
	v := g.guards(x)
	delete(g.walking, name)
	g.guarded[name] = v
	return v
}

// isCreationVar reports a variable named like a creation input: create or
// create_<x>.
func isCreationVar(ref string) bool {
	return ref == "var.create" || strings.HasPrefix(ref, "var.create_")
}

// localRefs returns the references of each local, by name.
func localRefs(ls []Local) map[string][]string {
	out := map[string][]string{}
	for _, l := range ls {
		out[l.Name] = exprRefs(&l.Value)
	}
	return out
}

// addCreationInputs adds to set the creation inputs of one revision's locals:
// the create and create_<x> variables a local reads alongside the gate, where
// the gate is var.putin_khuylo or a local that reads it, at any depth.
func addCreationInputs(refs map[string][]string, set map[string]bool) {
	gate := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, rs := range refs {
			if !gate[name] && slices.ContainsFunc(rs, func(r string) bool {
				l, ok := strings.CutPrefix(r, "local.")
				return r == "var.putin_khuylo" || (ok && gate[l])
			}) {
				gate[name], changed = true, true
			}
		}
	}
	for name := range gate {
		for _, r := range refs[name] {
			if isCreationVar(r) {
				set[r] = true
			}
		}
	}
}

// newGateGraph builds the graph of a root. Creation inputs come from the
// base and the head locals together, so a gate a change removes from a
// create_<x> local still names its input.
func newGateGraph(a *Area) *gateGraph {
	g := &gateGraph{creation: map[string]bool{}, refs: localRefs(a.Head.Locals), exprs: map[string]hclsyntax.Expression{},
		edited: map[string]bool{}, memo: map[string]*gateNode{}, guarded: map[string]bool{}, walking: map[string]bool{}}
	addCreationInputs(g.refs, g.creation)
	addCreationInputs(localRefs(a.Base.Locals), g.creation)
	base := map[string]string{}
	for _, l := range a.Base.Locals {
		base[l.Name] = l.Value.Text
	}
	for _, l := range a.Head.Locals {
		g.exprs[l.Name] = parseExpr(&l.Value)
		if t, ok := base[l.Name]; !ok || t != l.Value.Text {
			g.edited[l.Name] = true
		}
	}
	if len(g.creation) == 0 {
		g.creation["var.create"] = true
	}
	return g
}

// node answers gateNode for an expression and its references. Whether a
// creation input is reached follows every reference; whether the gate decides
// it follows the syntax, see guards. A local met again on the current walk
// adds nothing.
func (g *gateGraph) node(x hclsyntax.Expression, refs []string, walking map[string]bool) gateNode {
	var n gateNode
	for _, r := range refs {
		if g.creation[r] {
			n.reaches = true
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
		n.lostVia = append(n.lostVia, l.lostVia...)
		if g.edited[name] {
			n.via = append(n.via, name)
		}
	}
	n.open = n.reaches && (x == nil || !g.guards(x))
	if !n.open {
		n.lostVia = nil
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
	n := g.node(g.exprs[name], g.refs[name], walking)
	delete(walking, name)
	if n.open && g.edited[name] {
		n.lostVia = append(n.lostVia, name)
	}
	g.memo[name] = &n
	return n
}

// block answers gateNode for a count or for_each.
func (g *gateGraph) block(e *Expr) gateNode {
	return g.node(parseExpr(e), exprRefs(e), map[string]bool{})
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
			open = open || g.block(b.expr).open
		}
		if declared && !open {
			return Result{}
		}
		// With no variables.tf the finding names the root itself, a path
		// the change adds, so the scope keeps it.
		file := areaFile(a.Path, "variables.tf")
		if a.headTree.Entries[file] == nil {
			file = a.Path
		}
		f := Finding{RuleID: id, Severity: "MEDIUM", File: file,
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
		n := g.block(b.expr)
		edited := b.head || b.base == nil || b.base.Text != b.expr.Text
		if !n.open || (!edited && len(n.via) == 0) {
			continue
		}
		s := spot{b.file, b.expr.Range.Start.Line}
		what := "this block's count or for_each"
		if len(n.lostVia) > 0 {
			// The first edited local, by position, takes every block whose
			// gate it loses.
			l := locals[slices.MinFunc(n.lostVia, func(x, y string) int {
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
