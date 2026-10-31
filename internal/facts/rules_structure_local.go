package facts

import (
	"bytes"
	"cmp"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// structureLocalRules is the structure_local rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// Each is a candidate rule that reads one area's own files. A check emits a
// candidate for every block that matches its detector, added or not; the
// engine keeps those its scope judges. An area with no candidate is clean:
// each detector reads every block its rule names, and what it cannot decide,
// such as a try() fallback's type, it leaves to the confirmer as a candidate.
var structureLocalRules = []Rule{
	{ID: "structure.argument-order", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkArgumentOrder},
	{ID: "structure.arn-partition-hardcoded", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkARNHardcoded},
	{ID: "structure.multi-provider", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkMultiProvider},
	{ID: "structure.output-no-try", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkOutputNoTry},
	{ID: "structure.wrong-file", Tier: "candidate", Class: ClassConfig, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkWrongFile},
}

// evidence builds one candidate's evidence and records whether a cap bit.
type evidence struct {
	a        *Area
	ev       Evidence
	complete bool
}

func newEvidence(a *Area) *evidence {
	return &evidence{a: a, ev: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}, complete: true}
}

func (e *evidence) trim(s string) string {
	if len(s) > maxEvidenceText {
		e.complete = false
		return s[:maxEvidenceText]
	}
	return s
}

// line adds head line n of a file of the area as a source line.
func (e *evidence) line(file string, n int) {
	if len(e.ev.SourceLines) == maxEvidenceComments {
		e.complete = false
		return
	}
	full := areaFile(e.a.Path, file)
	var text string
	if en := e.a.headTree.Entries[full]; en != nil {
		if lines := bytes.Split(en.Data, []byte("\n")); n >= 1 && n <= len(lines) {
			text = strings.TrimRight(string(lines[n-1]), "\r")
		}
	}
	e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: "head", File: full, Line: n, Text: e.trim(text)})
}

func (e *evidence) head(s string) { h := e.trim(s); e.ev.Head = &h }

func (e *evidence) base(s string) { b := e.trim(s); e.ev.Base = &b }

func (e *evidence) candidate(rule, file string, line int, addr string) Candidate {
	return Candidate{RuleID: rule, File: areaFile(e.a.Path, file), Line: &line,
		Subject: subject(e.a.Path, e.trim(addr)), Evidence: e.ev, EvidenceComplete: e.complete}
}

// reparse parses an expression's source text again, at its own position, for
// the syntax tree the module model does not keep. The text parsed once
// already, so it parses again; nil only if it somehow does not.
func reparse(file string, x Expr) hclsyntax.Expression {
	// A heredoc needs the newline after its closing marker, which its
	// range leaves out.
	e, diags := hclsyntax.ParseExpression([]byte(x.Text+"\n"), file, x.Range.Start)
	if diags.HasErrors() {
		return nil
	}
	return e
}

// sourceOf returns the source text of a node inside a reparsed expression.
func sourceOf(x Expr, r hcl.Range) string {
	s, e := r.Start.Byte-x.Range.Start.Byte, r.End.Byte-x.Range.Start.Byte
	if s < 0 || e > len(x.Text) || s > e {
		return x.Text
	}
	return x.Text[s:e]
}

// structure.output-no-try: an output that can fail on an object that was
// not created, or whose fallback may change its type. A traversal outside
// every try() argument is a candidate, except a bare var.<name> with nothing
// after it, which cannot fail. A try() with one argument, or whose last
// argument is not null, is a candidate too: the engine cannot type the
// fallback. An output with no other traversal is clean.
func checkOutputNoTry(a *Area) Result {
	var res Result
	for _, o := range a.Head.Outputs {
		if o.Value == nil || outputSafe(o.File, *o.Value) {
			continue
		}
		e := newEvidence(a)
		e.line(o.File, o.Value.Range.Start.Line)
		e.head(o.Value.Text)
		res.Candidates = append(res.Candidates, e.candidate("structure.output-no-try", o.File, o.Line, "output."+o.Name))
	}
	return res
}

// outputSafe reports whether an output value needs no candidate. A value
// that does not parse again is not safe.
func outputSafe(file string, x Expr) bool {
	e := reparse(file, x)
	if e == nil {
		return false
	}
	w := &tryWalker{safe: true}
	_ = hclsyntax.Walk(e, w)
	return w.safe
}

// tryWalker walks an output value with its stack of nodes, so it knows the
// try() calls around a traversal and what follows a traversal.
type tryWalker struct {
	stack []hclsyntax.Node
	tries int
	safe  bool
}

func (w *tryWalker) Enter(n hclsyntax.Node) hcl.Diagnostics {
	switch x := n.(type) {
	case *hclsyntax.FunctionCallExpr:
		if x.Name == "try" {
			w.tries++
			if len(x.Args) < 2 {
				w.safe = false
			} else if lit, ok := x.Args[len(x.Args)-1].(*hclsyntax.LiteralValueExpr); !ok || !lit.Val.IsNull() {
				w.safe = false
			}
		}
	case *hclsyntax.ScopeTraversalExpr:
		if w.tries == 0 && !w.bareVar(x) {
			w.safe = false
		}
	}
	w.stack = append(w.stack, n)
	return nil
}

func (w *tryWalker) Exit(n hclsyntax.Node) hcl.Diagnostics {
	w.stack = w.stack[:len(w.stack)-1]
	if c, ok := n.(*hclsyntax.FunctionCallExpr); ok && c.Name == "try" {
		w.tries--
	}
	return nil
}

// bareVar reports whether t is var.<name> with no attribute, index or splat
// after it, in the traversal or in the expression that holds it.
func (w *tryWalker) bareVar(t *hclsyntax.ScopeTraversalExpr) bool {
	if len(t.Traversal) != 2 || t.Traversal.RootName() != "var" {
		return false
	}
	// Look past parentheses: (var.x)[0] and (var.x).a access var.x too.
	var child hclsyntax.Node = t
	i := len(w.stack) - 1
	for ; i >= 0; i-- {
		p, ok := w.stack[i].(*hclsyntax.ParenthesesExpr)
		if !ok {
			break
		}
		child = p
	}
	if i < 0 {
		return true
	}
	switch p := w.stack[i].(type) {
	case *hclsyntax.IndexExpr:
		return hclsyntax.Node(p.Collection) != child
	case *hclsyntax.SplatExpr:
		return hclsyntax.Node(p.Source) != child
	case *hclsyntax.RelativeTraversalExpr:
		return hclsyntax.Node(p.Source) != child
	}
	return true
}

// structure.multi-provider: a provider block with an alias, a provider
// argument on a resource, data, ephemeral or import block or on a data block
// inside a check block, or a providers argument on a module block. Example
// areas are not_applicable by kind.
func checkMultiProvider(a *Area) Result {
	const id = "structure.multi-provider"
	var res Result
	for _, b := range topBlocks(a.Head) {
		if !strings.HasPrefix(b.addr, "provider.") || b.body == nil {
			continue
		}
		for _, at := range b.body.Attributes {
			if at.Name != "alias" {
				continue
			}
			e := newEvidence(a)
			e.line(b.File, at.Range.Start.Line)
			if s, err := strconv.Unquote(at.Value.Text); err == nil {
				e.head(s)
			} else {
				e.head(at.Value.Text)
			}
			res.Candidates = append(res.Candidates, e.candidate(id, b.File, b.Line, b.addr))
		}
	}
	arg := func(file, addr, name string, body Body) {
		for _, at := range body.Attributes {
			if at.Name == name {
				e := newEvidence(a)
				e.line(file, at.Range.Start.Line)
				e.head(at.Value.Text)
				res.Candidates = append(res.Candidates, e.candidate(id, file, at.Range.Start.Line, addr+"."+name))
			}
		}
	}
	for _, r := range a.Head.Resources {
		arg(r.File, coverageBlockAddr(r), "provider", r.Body)
	}
	for _, c := range a.Head.ModuleCalls {
		arg(c.File, "module."+c.Name, "providers", c.Body)
	}
	for _, o := range a.Head.Other {
		addr := strings.Join(append([]string{o.Type}, o.Labels...), ".")
		switch o.Type {
		case "ephemeral", "import":
			arg(o.File, addr, "provider", o.Body)
		case "check":
			// A check block's scoped data blocks, named as the index names them.
			n := 0
			for _, nb := range o.Body.Blocks {
				if nb.Type == "data" && !nb.Dynamic {
					arg(o.File, addr+".data["+strconv.Itoa(n)+"]", "provider", nb.Body)
					n++
				}
			}
		}
	}
	return res
}

// archetypeFiles are the files the service archetypes name for a group of
// blocks, with the test for a block of that group. The identity group is the
// maintainer skill's references/service-archetypes.md, "iam.tf (Identity /
// Access Management)": it names aws_iam_role, aws_iam_policy_document,
// google_service_account, google_project_iam_member, azurerm_role_assignment
// and azurerm_user_assigned_identity, read here as their type families. Locals
// go in locals.tf. Logging has no fixed file: the archetypes allow it in
// main.tf or logging.tf, so logging.tf counts as a feature file.
var archetypeFiles = []struct {
	file   string
	member func(kind, typ string) bool
}{
	{"iam.tf", identityType},
	{"locals.tf", func(kind, _ string) bool { return kind == "locals" }},
}

func identityType(kind, typ string) bool {
	return kind == "resource" && (strings.HasPrefix(typ, "aws_iam_") || strings.HasPrefix(typ, "google_service_account") ||
		strings.HasPrefix(typ, "google_") && strings.Contains(typ, "_iam_") ||
		strings.HasPrefix(typ, "azurerm_role_") || typ == "azurerm_user_assigned_identity")
}

// fixedFiles are the .tf files whose contents the profile and the archetypes
// fix. Any other .tf file of an area is a feature file.
var fixedFiles = []string{"main.tf", "variables.tf", "outputs.tf", "versions.tf", "iam.tf", "locals.tf"}

// structure.wrong-file: a block of a group placed outside the file the
// archetype names for it, when the directory has that file at the head. A
// directory without the file gets no candidate for that group. Data sources
// count with their resources: data.aws_iam_policy_document is identity.
//
// A feature file, <feature>.tf, has no fixed members, so whether a resource
// in main.tf belongs there is not decidable: with one present, every resource
// or data block in main.tf that no group claims is a candidate, with the
// feature files as head evidence. A feature file is a regular file; a
// directory or a link named <feature>.tf is not one.
func checkWrongFile(a *Area) Result {
	const id = "structure.wrong-file"
	var res Result
	var features []string
	var paths []string
	if a.headTree != nil {
		paths = a.headTree.Paths
	}
	for _, p := range paths {
		name := path.Base(p)
		if path.Dir(p) == a.Path && strings.HasSuffix(name, ".tf") && !slices.Contains(fixedFiles, name) {
			if en := a.headTree.Entries[p]; en.Kind == KindFile {
				features = append(features, name)
			}
		}
	}
	add := func(file string, line int, addr, kind, typ string) bool {
		for _, g := range archetypeFiles {
			if !g.member(kind, typ) {
				continue
			}
			// A block of a group is claimed by it: silent in its own file or
			// when the directory has no such file, as the rule text says.
			if en := a.headEntry(g.file); file == g.file || en == nil || en.Kind != KindFile {
				return true
			}
			e := newEvidence(a)
			e.line(file, line)
			e.head(g.file)
			res.Candidates = append(res.Candidates, e.candidate(id, file, line, addr))
			return true
		}
		return false
	}
	for _, r := range a.Head.Resources {
		if add(r.File, r.Line, coverageBlockAddr(r), "resource", r.Type) || r.File != "main.tf" || len(features) == 0 {
			continue
		}
		e := newEvidence(a)
		e.line(r.File, r.Line)
		e.head(strings.Join(features, ", "))
		res.Candidates = append(res.Candidates, e.candidate(id, r.File, r.Line, coverageBlockAddr(r)))
	}
	// Each local by itself: every locals block shares the one address, so a
	// new block in a module that has one reads as an edit, a new local as new.
	for _, l := range a.Head.Locals {
		add(l.File, l.Line, "local."+l.Name, "locals", "")
	}
	return res
}

// structure.argument-order: a resource block whose first-position argument,
// count or for_each in the profile, follows another argument or block, or
// whose last-position argument, tags or labels, has another after it. The
// positions come from the profile's argument-order constants.
func checkArgumentOrder(a *Area) Result {
	if res, ok := needProfile(a); !ok {
		return res
	}
	first, last := a.Profile.ArgumentOrder.First, a.Profile.ArgumentOrder.Last
	var res Result
	for _, r := range a.Head.Resources {
		if r.Mode != "resource" {
			continue
		}
		items := bodyItems(r.Body)
		bad := misplaced(items, first, last)
		if len(bad) == 0 {
			continue
		}
		e := newEvidence(a)
		for _, l := range bad {
			e.line(r.File, l)
		}
		b := baseResource(a, r)
		if b != nil {
			e.base(itemNames(bodyItems(b.Body)))
		}
		e.head(itemNames(items))
		addr := coverageBlockAddr(r)
		c := e.candidate("structure.argument-order", r.File, r.Line, addr)
		// The base block of the same address, so the same subject, breaks
		// the order too: the defect is older than the change.
		c.PredatesChange = b != nil && len(misplaced(bodyItems(b.Body), first, last)) > 0
		res.Candidates = append(res.Candidates, c)
	}
	return res
}

// misplaced returns the lines of the items out of order: a first-position
// item after another item, or a last-position item before another one.
func misplaced(items []bodyItem, first, last []string) []int {
	var bad []int
	for i, it := range items {
		before := slices.ContainsFunc(items[:i], func(o bodyItem) bool { return !slices.Contains(first, o.name) })
		after := slices.ContainsFunc(items[i+1:], func(o bodyItem) bool { return !slices.Contains(last, o.name) })
		if slices.Contains(first, it.name) && before || slices.Contains(last, it.name) && after {
			bad = append(bad, it.line)
		}
	}
	return bad
}

type bodyItem struct {
	name  string
	line  int
	start int
}

// bodyItems lists the arguments and nested blocks of a body in source order,
// a nested block by its type.
func bodyItems(b Body) []bodyItem {
	var out []bodyItem
	for _, at := range b.Attributes {
		out = append(out, bodyItem{at.Name, at.Range.Start.Line, at.Range.Start.Byte})
	}
	for _, nb := range b.Blocks {
		out = append(out, bodyItem{nb.Type, nb.Range.Start.Line, nb.Range.Start.Byte})
	}
	slices.SortFunc(out, func(x, y bodyItem) int { return cmp.Compare(x.start, y.start) })
	return out
}

func itemNames(items []bodyItem) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.name
	}
	return strings.Join(names, ", ")
}

var (
	partitionField = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
	regionField    = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	accountField   = regexp.MustCompile(`^[0-9]{12}$`)
)

// structure.arn-partition-hardcoded: a string in the configuration holding an
// ARN whose partition is a literal, or whose region or account is a literal
// region or a 12-digit account ID, or a format verb, which format() may fill
// with either. Strings are read from the syntax tree, so an ARN in a comment
// is never one. One candidate per string, at the line its arn: is on, with
// the argument as subject; an ARN in a variable, an output, a local, a
// removed block or a nested terraform block is subject to that block, and a
// terraform argument to its name. An expression that does not parse again is
// a candidate, so the rule fails closed.
//
// shortcut: an ARN assembled by join(":", [...]) from list elements is not
// read; read the join's literal list when such a case turns up.
func checkARNHardcoded(a *Area) Result {
	var res Result
	scan := func(file, addr string, x Expr) {
		add := func(line int, head string) {
			ev := newEvidence(a)
			ev.line(file, line)
			ev.head(head)
			res.Candidates = append(res.Candidates, ev.candidate("structure.arn-partition-hardcoded", file, line, addr))
		}
		e := reparse(file, x)
		if e == nil {
			add(x.Range.Start.Line, x.Text)
			return
		}
		_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
			if t, ok := n.(*hclsyntax.TemplateExpr); ok {
				if line := hardcodedARNLine(t); line > 0 {
					add(line, sourceOf(x, t.SrcRange))
				}
			}
			return nil
		})
	}
	for _, b := range topBlocks(a.Head) {
		switch {
		case b.args != nil: // variable or output: the block is the subject
			walkBody(*b.args, b.addr, false, func(sub string, x Expr) { scan(b.File, sub, x) })
		case b.body != nil:
			walkBody(*b.body, b.addr, true, func(sub string, x Expr) { scan(b.File, sub, x) })
		}
	}
	for _, l := range a.Head.Locals {
		scan(l.File, "local."+l.Name, l.Value)
	}
	for _, t := range a.Head.Terraform {
		for _, at := range t.Body.Attributes {
			scan(t.File, at.Name, at.Value)
		}
		for _, nb := range t.Body.Blocks {
			walkBody(nb.Body, nb.Type, false, func(sub string, x Expr) { scan(t.File, sub, x) })
		}
	}
	for _, rm := range a.Head.Removed {
		walkBody(rm.Body, "removed."+fromAddr(rm.FromAddr, rm.From), false, func(sub string, x Expr) { scan(rm.File, sub, x) })
	}
	return res
}

// walkBody calls fn for every expression of a body and its nested blocks,
// with the subject the index gives it when args is set, or addr otherwise.
func walkBody(b Body, addr string, args bool, fn func(subject string, x Expr)) {
	for _, at := range b.Attributes {
		sub := addr
		if args {
			sub += "." + at.Name
		}
		fn(sub, at.Value)
	}
	seen := map[string]int{}
	for _, nb := range b.Blocks {
		sub := addr
		if args {
			sub += "." + nb.Type
			if !nb.Dynamic {
				sub += "[" + strconv.Itoa(seen[nb.Type]) + "]"
				seen[nb.Type]++
			}
		}
		if nb.ForEach != nil {
			fn(sub, *nb.ForEach)
		}
		walkBody(nb.Body, sub, args, fn)
	}
}

// hardcodedARNLine returns the line of the first ARN in a string template
// with a literal or format-verb partition, region or account, or 0 for none.
// The template's text has every interpolation as NUL; the line is that of the
// literal part the ARN starts in plus the newlines before it in that part.
func hardcodedARNLine(t *hclsyntax.TemplateExpr) int {
	var b strings.Builder
	type part struct{ start, line int }
	var parts []part
	for _, p := range t.Parts {
		if lit, ok := p.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
			parts = append(parts, part{b.Len(), lit.SrcRange.Start.Line})
			b.WriteString(lit.Val.AsString())
		} else {
			b.WriteByte(0)
		}
	}
	s := b.String()
	i := hardcodedARN(s)
	if i < 0 {
		return 0
	}
	line := t.SrcRange.Start.Line
	for _, p := range parts {
		if p.start <= i {
			line = p.line + strings.Count(s[p.start:i], "\n")
		}
	}
	return line
}

// hardcodedARN returns the offset of the first ARN in s,
// arn:<partition>:<service>:<region>:<account>:<resource>, with a literal
// partition, a literal region or account ID, or a format verb in one of the
// three, or -1 for none.
func hardcodedARN(s string) int {
	for i := 0; ; i++ {
		j := strings.Index(s[i:], "arn:")
		if j < 0 {
			return -1
		}
		i += j
		if i > 0 && isWordByte(s[i-1]) {
			continue
		}
		f := strings.SplitN(s[i:], ":", 6)
		if len(f) < 6 {
			continue
		}
		verb := strings.Contains(f[1], "%") || strings.Contains(f[3], "%") || strings.Contains(f[4], "%")
		if verb || partitionField.MatchString(f[1]) || regionField.MatchString(f[3]) || accountField.MatchString(f[4]) {
			return i
		}
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
