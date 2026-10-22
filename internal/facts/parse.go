package facts

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Module holds the parsed facts of one directory. Directories are never merged.
type Module struct {
	Dir string
	// Unsupported lists why config-semantic rules cannot be decided here:
	// parse errors, JSON or override files, links or unreadable .tf files.
	Unsupported []string
	// unsupportedCodes holds the reason code of each Unsupported entry.
	unsupportedCodes []string

	Variables         []Variable
	Outputs           []Output
	RequiredProviders []string
	Providers         []Provider
	Resources         []Resource
	Moved             []Moved
	Removed           []Removed
	ModuleCalls       []ModuleCall

	varNames map[string]bool
	outNames map[string]bool
}

type Variable struct {
	Name           string
	File           string
	Line           int
	HasType        bool
	TypeText       string    // source text of the type expression
	TypeUsesAny    bool      // the type expression names `any` anywhere
	Comments       []Comment // comments inside the block or on the line above it
	HasDescription bool      // present and not an empty string literal
}

// Comment is one comment token and the line it starts on.
type Comment struct {
	Line int
	Text string
}

type Output struct {
	Name           string
	File           string
	Line           int
	HasDescription bool
}

type Provider struct {
	Name  string
	Alias string
	File  string
	Line  int
}

// Resource is a resource or data block, with meta-arguments kept apart from
// the provider attributes and nested blocks.
type Resource struct {
	Mode       string // "resource" or "data"
	Type       string
	Name       string
	File       string
	Line       int
	Meta       []string
	Attributes []string
	Blocks     []string // nested block types; a dynamic block counts as its label
}

type Moved struct {
	From, To string // source text of the expressions
	File     string
	Line     int
}

type Removed struct {
	From string
	File string
	Line int
}

type ModuleCall struct {
	Name        string
	Source      string
	SourceRange hcl.Range
	File        string
	Line        int
	Meta        []string
	Attributes  []string
}

var resourceMetaAttrs = map[string]bool{"count": true, "for_each": true, "depends_on": true, "provider": true}
var resourceMetaBlocks = map[string]bool{"lifecycle": true, "provisioner": true, "connection": true}
var moduleMetaAttrs = map[string]bool{"source": true, "version": true, "count": true, "for_each": true, "depends_on": true, "providers": true}

// unsupportedName reports why a file name makes config-semantic rules
// undecidable for its directory, or "" when it does not.
func unsupportedName(name string) string {
	switch {
	case strings.HasSuffix(name, ".tf.json"):
		return name + " is a JSON configuration file"
	case name == "override.tf" || strings.HasSuffix(name, "_override.tf"):
		return name + " is an override file"
	}
	return ""
}

// ignoredName reports a file Terraform itself skips: hidden files and
// editor backup and lock files.
func ignoredName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") ||
		(strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#"))
}

// ParseDir parses the *.tf files directly in dir.
func ParseDir(t *Tree, dir string, lim Limits) (*Module, error) {
	m := &Module{Dir: dir, varNames: map[string]bool{}, outNames: map[string]bool{}}
	for _, e := range t.DirEntries(dir) {
		if err := lim.expired(); err != nil {
			return nil, err
		}
		name := path.Base(e.Path)
		if ignoredName(name) {
			continue
		}
		if r := unsupportedName(name); r != "" {
			m.unsupported(ReasonUnsupportedFile, r)
			continue
		}
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		if e.Kind != KindFile {
			m.unsupported(kindCode(e.Kind), fmt.Sprintf("%s is %s", name, kindText(e.Kind)))
			continue
		}
		if err := parseFile(m, name, e.Data, lim); err != nil {
			code := ReasonParseError
			if errors.As(err, new(limitError)) {
				code = ReasonLimitExceeded
			}
			m.unsupported(code, fmt.Sprintf("%s: %v", name, err))
		}
	}
	return m, nil
}

// unsupported records why config-semantic rules cannot be decided here, with
// its reason code.
func (m *Module) unsupported(code, text string) {
	m.Unsupported = append(m.Unsupported, text)
	m.unsupportedCodes = append(m.unsupportedCodes, code)
}

// reasonCode is the code of the first Unsupported entry, or "" for a
// Module that records none, which CheckManifest refuses.
func (m *Module) reasonCode() string {
	if len(m.unsupportedCodes) == 0 {
		return ""
	}
	return m.unsupportedCodes[0]
}

// limitError is a parse refused because the file is over a token, nesting or
// unary operator limit.
type limitError string

func (e limitError) Error() string { return string(e) }

// kindCode is the reason code of an entry that is not a regular file.
func kindCode(k string) string {
	switch k {
	case KindSymlink:
		return ReasonSymlink
	case KindOversize:
		return ReasonLimitExceeded
	}
	return ReasonUnsupportedFile
}

func kindText(k string) string {
	switch k {
	case KindSymlink:
		return "a symbolic link"
	case KindSpecial:
		return "a special file"
	case KindOversize:
		return "over the file size limit"
	}
	return "unreadable"
}

// parseFile adds one file's facts to m. A panic in the parser or the walk
// is recovered and reported as an error for this file only.
func parseFile(m *Module, name string, src []byte, lim Limits) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal error while parsing")
		}
	}()
	tokens, diags := hclsyntax.LexConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("parse error")
	}
	// The parser recurses on nesting and on unary operators, and a stack
	// overflow is fatal, so both are bounded before it runs.
	if len(tokens) > lim.MaxTokens {
		return limitError(fmt.Sprintf("%d tokens over the limit %d", len(tokens), lim.MaxTokens))
	}
	if d := nestingDepth(tokens); d > lim.MaxDepth {
		return limitError(fmt.Sprintf("nesting depth %d over the limit %d", d, lim.MaxDepth))
	}
	if n := prefixRun(tokens); n > lim.MaxPrefixOps {
		return limitError(fmt.Sprintf("%d consecutive unary operators over the limit %d", n, lim.MaxPrefixOps))
	}
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("parse error")
	}
	var comments []hclsyntax.Token
	for _, tk := range tokens {
		if tk.Type == hclsyntax.TokenComment {
			comments = append(comments, tk)
		}
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return fmt.Errorf("parse error: unexpected body type %T", f.Body)
	}
	collect(m, name, src, body, comments)
	return nil
}

func nestingDepth(tokens hclsyntax.Tokens) int {
	depth, deepest := 0, 0
	for _, tk := range tokens {
		switch tk.Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack, hclsyntax.TokenOParen,
			hclsyntax.TokenTemplateInterp, hclsyntax.TokenTemplateControl, hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			depth++
			deepest = max(deepest, depth)
		case hclsyntax.TokenCBrace, hclsyntax.TokenCBrack, hclsyntax.TokenCParen,
			hclsyntax.TokenTemplateSeqEnd, hclsyntax.TokenCQuote, hclsyntax.TokenCHeredoc:
			if depth > 0 {
				depth--
			}
		}
	}
	return deepest
}

// prefixRun returns the longest run of consecutive `!` or `-` tokens.
func prefixRun(tokens hclsyntax.Tokens) int {
	run, longest := 0, 0
	for _, tk := range tokens {
		if tk.Type == hclsyntax.TokenBang || tk.Type == hclsyntax.TokenMinus {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

func collect(m *Module, file string, src []byte, body *hclsyntax.Body, comments []hclsyntax.Token) {
	for _, b := range body.Blocks {
		line := b.TypeRange.Start.Line
		switch {
		case b.Type == "variable" && len(b.Labels) == 1:
			v := Variable{Name: b.Labels[0], File: file, Line: line}
			if a, ok := b.Body.Attributes["type"]; ok {
				v.HasType = true
				v.TypeText = exprText(src, a.Expr)
				v.TypeUsesAny = usesAny(a.Expr)
			}
			v.HasDescription = hasText(b.Body.Attributes["description"])
			// comments are in source order, so search for the first one in range.
			i := sort.Search(len(comments), func(i int) bool { return comments[i].Range.Start.Line >= line-1 })
			for ; i < len(comments) && comments[i].Range.Start.Line <= b.Body.SrcRange.End.Line; i++ {
				v.Comments = append(v.Comments, Comment{Line: comments[i].Range.Start.Line, Text: strings.TrimRight(string(comments[i].Bytes), "\r\n")})
			}
			m.Variables = append(m.Variables, v)
			m.varNames[v.Name] = true
		case b.Type == "output" && len(b.Labels) == 1:
			m.Outputs = append(m.Outputs, Output{Name: b.Labels[0], File: file, Line: line,
				HasDescription: hasText(b.Body.Attributes["description"])})
			m.outNames[b.Labels[0]] = true
		case b.Type == "provider" && len(b.Labels) == 1:
			p := Provider{Name: b.Labels[0], File: file, Line: line}
			if a, ok := b.Body.Attributes["alias"]; ok {
				p.Alias = exprText(src, a.Expr)
			}
			m.Providers = append(m.Providers, p)
		case b.Type == "terraform":
			for _, rb := range b.Body.Blocks {
				if rb.Type == "required_providers" {
					for name := range rb.Body.Attributes {
						m.RequiredProviders = append(m.RequiredProviders, name)
					}
				}
			}
		case (b.Type == "resource" || b.Type == "data") && len(b.Labels) == 2:
			m.Resources = append(m.Resources, resource(b, file))
		case b.Type == "moved":
			m.Moved = append(m.Moved, Moved{From: attrText(src, b.Body, "from"), To: attrText(src, b.Body, "to"), File: file, Line: line})
		case b.Type == "removed":
			m.Removed = append(m.Removed, Removed{From: attrText(src, b.Body, "from"), File: file, Line: line})
		case b.Type == "module" && len(b.Labels) == 1:
			m.ModuleCalls = append(m.ModuleCalls, moduleCall(b, file))
		}
	}
	sortStrings(m.RequiredProviders)
}

func resource(b *hclsyntax.Block, file string) Resource {
	r := Resource{Mode: b.Type, Type: b.Labels[0], Name: b.Labels[1], File: file, Line: b.TypeRange.Start.Line}
	for name := range b.Body.Attributes {
		if resourceMetaAttrs[name] {
			r.Meta = append(r.Meta, name)
		} else {
			r.Attributes = append(r.Attributes, name)
		}
	}
	for _, nb := range b.Body.Blocks {
		switch {
		case resourceMetaBlocks[nb.Type]:
			r.Meta = append(r.Meta, nb.Type)
		case nb.Type == "dynamic" && len(nb.Labels) == 1:
			r.Meta = append(r.Meta, "dynamic")
			r.Blocks = append(r.Blocks, nb.Labels[0])
		default:
			r.Blocks = append(r.Blocks, nb.Type)
		}
	}
	sortStrings(r.Meta, r.Attributes, r.Blocks)
	return r
}

func moduleCall(b *hclsyntax.Block, file string) ModuleCall {
	c := ModuleCall{Name: b.Labels[0], File: file, Line: b.TypeRange.Start.Line}
	for name, a := range b.Body.Attributes {
		if moduleMetaAttrs[name] {
			c.Meta = append(c.Meta, name)
		} else {
			c.Attributes = append(c.Attributes, name)
		}
		if name == "source" {
			c.SourceRange = a.Expr.Range()
			c.Source, _ = literalString(a.Expr)
		}
	}
	sortStrings(c.Meta, c.Attributes)
	return c
}

// usesAny reports whether a type expression names the `any` keyword.
func usesAny(e hclsyntax.Expression) bool {
	found := false
	// The visitor returns no diagnostics, so VisitAll has none to report.
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok && len(st.Traversal) == 1 && st.Traversal.RootName() == "any" {
			found = true
		}
		return nil
	})
	return found
}

// hasText reports whether an attribute is present and not null, an empty
// string or a blank string. Expressions are read, never evaluated: a deep
// expression would overflow the evaluator's stack. Anything other than a
// literal counts as text.
func hasText(a *hclsyntax.Attribute) bool {
	if a == nil {
		return false
	}
	if lit, ok := a.Expr.(*hclsyntax.LiteralValueExpr); ok && lit.Val.IsNull() {
		return false
	}
	if s, ok := literalString(a.Expr); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// literalString returns the text of a string with no interpolation.
func literalString(e hclsyntax.Expression) (string, bool) {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String && x.Val.IsKnown() && !x.Val.IsNull() {
			return x.Val.AsString(), true
		}
	case *hclsyntax.TemplateExpr:
		var b strings.Builder
		for _, p := range x.Parts {
			lit, ok := p.(*hclsyntax.LiteralValueExpr)
			if !ok || lit.Val.Type() != cty.String || lit.Val.IsNull() {
				return "", false
			}
			b.WriteString(lit.Val.AsString())
		}
		return b.String(), true
	}
	return "", false
}

func attrText(src []byte, body *hclsyntax.Body, name string) string {
	if a, ok := body.Attributes[name]; ok {
		return exprText(src, a.Expr)
	}
	return ""
}

func exprText(src []byte, e hclsyntax.Expression) string {
	return string(e.Range().SliceBytes(src))
}
