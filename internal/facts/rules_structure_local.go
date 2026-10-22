package facts

import (
	"bytes"
	"cmp"
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

// structure.output-no-try: an output with no try() fallback, or with a
// fallback that may change its type. An output that reads only input
// variables, or nothing, cannot fail on an object that was not created and
// is skipped. A try() whose last argument is not null may change the type;
// the engine cannot type it, so it is a candidate too.
func checkOutputNoTry(a *Area) Result {
	var res Result
	for _, o := range a.Head.Outputs {
		if o.Value == nil || !slices.ContainsFunc(o.References, func(r string) bool { return !strings.HasPrefix(r, "var.") }) {
			continue
		}
		if o.UsesTry && nullFallbacks(o.File, *o.Value) {
			continue
		}
		e := newEvidence(a)
		e.line(o.File, o.Value.Range.Start.Line)
		e.head(o.Value.Text)
		res.Candidates = append(res.Candidates, e.candidate("structure.output-no-try", o.File, o.Line, "output."+o.Name))
	}
	return res
}

// nullFallbacks reports whether every try() in the expression ends in null.
func nullFallbacks(file string, x Expr) bool {
	e := reparse(file, x)
	if e == nil {
		return false
	}
	ok := true
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if c, is := n.(*hclsyntax.FunctionCallExpr); is && c.Name == "try" {
			if len(c.Args) < 2 {
				ok = false
				return nil
			}
			lit, isLit := c.Args[len(c.Args)-1].(*hclsyntax.LiteralValueExpr)
			ok = ok && isLit && lit.Val.IsNull()
		}
		return nil
	})
	return ok
}

// structure.multi-provider: a provider block with an alias, a provider
// argument on a resource or data block, or a providers argument on a module
// block. Example areas are not_applicable by kind.
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
		arg(r.File, resourceAddr(r), "provider", r.Body)
	}
	for _, c := range a.Head.ModuleCalls {
		arg(c.File, "module."+c.Name, "providers", c.Body)
	}
	return res
}

func resourceAddr(r Resource) string {
	if r.Mode == "data" {
		return "data." + r.Type + "." + r.Name
	}
	return r.Type + "." + r.Name
}

// archetypeFiles are the files the service archetypes name for a group of
// blocks, with the test for a block of that group: identity resources in
// iam.tf, logging resources in logging.tf, locals in locals.tf. A
// feature file, <feature>.tf, has no fixed name or members, so no block is
// out of place for it.
var archetypeFiles = []struct {
	file   string
	member func(kind, typ string) bool
}{
	{"iam.tf", func(kind, typ string) bool {
		return kind == "resource" && (strings.HasPrefix(typ, "aws_iam_") || strings.HasPrefix(typ, "google_service_account") ||
			strings.HasPrefix(typ, "google_") && strings.Contains(typ, "_iam_") ||
			strings.HasPrefix(typ, "azurerm_role_") || typ == "azurerm_user_assigned_identity")
	}},
	{"logging.tf", func(kind, typ string) bool {
		return kind == "resource" && (strings.HasPrefix(typ, "aws_cloudwatch_log_") || strings.HasPrefix(typ, "google_logging_") ||
			typ == "azurerm_monitor_diagnostic_setting")
	}},
	{"locals.tf", func(kind, _ string) bool { return kind == "locals" }},
}

// structure.wrong-file: a block of a group placed outside the file the
// archetype names for it, when the directory has that file at the head. A
// directory without the file gets no candidate for that group. Data sources
// count with their resources: data.aws_iam_policy_document is identity.
func checkWrongFile(a *Area) Result {
	var res Result
	add := func(file string, line int, addr, kind, typ string) {
		for _, g := range archetypeFiles {
			if file == g.file || !g.member(kind, typ) {
				continue
			}
			if en := a.headEntry(g.file); en == nil || en.Kind != KindFile {
				continue
			}
			e := newEvidence(a)
			e.line(file, line)
			e.head(g.file)
			res.Candidates = append(res.Candidates, e.candidate("structure.wrong-file", file, line, addr))
		}
	}
	for _, r := range a.Head.Resources {
		add(r.File, r.Line, resourceAddr(r), "resource", r.Type)
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
		var bad []int
		for i, it := range items {
			before := slices.ContainsFunc(items[:i], func(o bodyItem) bool { return !slices.Contains(first, o.name) })
			after := slices.ContainsFunc(items[i+1:], func(o bodyItem) bool { return !slices.Contains(last, o.name) })
			if slices.Contains(first, it.name) && before || slices.Contains(last, it.name) && after {
				bad = append(bad, it.line)
			}
		}
		if len(bad) == 0 {
			continue
		}
		e := newEvidence(a)
		for _, l := range bad {
			e.line(r.File, l)
		}
		if b := baseResource(a, r); b != nil {
			e.base(itemNames(bodyItems(b.Body)))
		}
		e.head(itemNames(items))
		addr := resourceAddr(r)
		res.Candidates = append(res.Candidates, e.candidate("structure.argument-order", r.File, r.Line, addr))
	}
	return res
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

// baseResource returns the base block with the address of r, or nil.
func baseResource(a *Area, r Resource) *Resource {
	if a.Base == nil {
		return nil
	}
	for i, b := range a.Base.Resources {
		if resourceAddr(b) == resourceAddr(r) {
			return &a.Base.Resources[i]
		}
	}
	return nil
}

var (
	partitionField = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
	regionField    = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	accountField   = regexp.MustCompile(`^[0-9]{12}$`)
)

// structure.arn-partition-hardcoded: a string in the configuration holding an
// ARN whose partition is a literal, or whose region or account is a literal
// region or a 12-digit account ID. Strings are read from the syntax tree, so
// an ARN in a comment is never one. One candidate per string, at its line,
// with the argument as subject; an ARN in a variable, an output or a local is
// subject to that block.
func checkARNHardcoded(a *Area) Result {
	var res Result
	scan := func(file, addr string, x Expr) {
		e := reparse(file, x)
		if e == nil {
			return
		}
		_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
			t, ok := n.(*hclsyntax.TemplateExpr)
			if !ok || !hardcodedARN(templateText(t)) {
				return nil
			}
			ev := newEvidence(a)
			line := t.SrcRange.Start.Line
			ev.line(file, line)
			ev.head(sourceOf(x, t.SrcRange))
			res.Candidates = append(res.Candidates, ev.candidate("structure.arn-partition-hardcoded", file, line, addr))
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

// templateText is a string template's text with every interpolation as NUL.
func templateText(t *hclsyntax.TemplateExpr) string {
	var b strings.Builder
	for _, p := range t.Parts {
		if lit, ok := p.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String && lit.Val.IsKnown() && !lit.Val.IsNull() {
			b.WriteString(lit.Val.AsString())
		} else {
			b.WriteByte(0)
		}
	}
	return b.String()
}

// hardcodedARN reports whether a string holds an ARN,
// arn:<partition>:<service>:<region>:<account>:<resource>, with a literal
// partition, or a literal region or account ID.
func hardcodedARN(s string) bool {
	for i := 0; ; i++ {
		j := strings.Index(s[i:], "arn:")
		if j < 0 {
			return false
		}
		i += j
		if i > 0 && isWordByte(s[i-1]) {
			continue
		}
		f := strings.SplitN(s[i:], ":", 6)
		if len(f) == 6 && (partitionField.MatchString(f[1]) || regionField.MatchString(f[3]) || accountField.MatchString(f[4])) {
			return true
		}
	}
}

func isWordByte(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
