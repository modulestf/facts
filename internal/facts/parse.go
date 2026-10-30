package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
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
	Locals            []Local
	Terraform         []Terraform
	RequiredProviders []string // local names, sorted
	Providers         []Provider
	Resources         []Resource
	Moved             []Moved
	Removed           []Removed
	ModuleCalls       []ModuleCall
	// Other holds the top-level blocks with no model of their own: locals,
	// whose arguments are in Locals, and check, import or any other type.
	Other []OtherBlock

	varNames map[string]bool
	outNames map[string]bool
}

// Expr is one expression as written: its source text and its range. It is
// read, never evaluated.
type Expr struct {
	Text  string
	Range hcl.Range
}

// Attribute is one argument of a body. Range covers the name through the
// value; Value.Range covers the value only.
type Attribute struct {
	Name  string
	Range hcl.Range
	Value Expr
}

// Body is a block body: its arguments and its nested blocks, both in source
// order.
type Body struct {
	Attributes []Attribute
	Blocks     []NestedBlock
}

// NestedBlock is a block inside a top-level block. A dynamic block has
// Dynamic set, Type is the type of the blocks it generates, ForEach and
// Iterator are its own arguments and Body is its content.
type NestedBlock struct {
	Type     string
	Labels   []string
	Dynamic  bool
	ForEach  *Expr
	Iterator string
	Range    hcl.Range
	Body     Body
}

// Span is where a top-level block is, with the SHA-256 of its source bytes.
// The hashed bytes are those of the block's range: from the block type
// keyword through the closing brace, with no comment or blank line before
// it. For a Local they are the argument, from its name through its value.
// The bytes are hashed as read, so a CRLF and an LF copy of a block differ.
type Span struct {
	File  string
	Line  int
	Range hcl.Range
	Hash  string
}

// spanOf returns the span of the source range r that starts on line.
func spanOf(src []byte, file string, line int, r hcl.Range) Span {
	sum := sha256.Sum256(r.SliceBytes(src))
	return Span{File: file, Line: line, Range: r, Hash: hex.EncodeToString(sum[:])}
}

type Variable struct {
	Span
	Name           string
	HasType        bool
	TypeText       string    // source text of the type expression
	TypeUsesAny    bool      // the type expression names `any` anywhere
	Comments       []Comment // comments inside the block or on the line above it
	HasDescription bool      // present and not an empty string literal
	Default        *Expr
	Nullable       *Expr
	Sensitive      *Expr
	Validations    []Validation
	// TypeAttrs are the object attributes of the type, at any depth.
	TypeAttrs []TypeAttr
	// Body holds every argument and the validation blocks as written.
	Body Body
}

// Validation is one validation block of a variable.
type Validation struct {
	Condition    *Expr
	ErrorMessage *Expr
	Range        hcl.Range
}

// TypeAttr is one attribute of an object type constraint. Path joins the
// attribute names from the outermost object with dots; collection types in
// between add nothing. Optional is set for optional(), with Default its
// second argument when given.
type TypeAttr struct {
	Path     string
	Optional bool
	Default  *Expr
	Range    hcl.Range // the attribute's key through its type
}

// Comment is one comment token and the line it starts on.
type Comment struct {
	Line int
	Text string
}

type Output struct {
	Span
	Name           string
	HasDescription bool
	Value          *Expr
	Sensitive      *Expr
	// References are the addresses the value reads, such as var.x,
	// module.y, data.t.n or t.n, sorted and without duplicates.
	References []string
	UsesTry    bool // the value calls try()
	Body       Body // every argument as written
}

// Local is one named value of a locals block.
type Local struct {
	Span
	Name  string
	Value Expr
}

// OtherBlock is a top-level block with no model of its own. Body is empty
// for a locals block.
type OtherBlock struct {
	Span
	Type   string
	Labels []string
	Body   Body
}

// Terraform is a terraform block.
type Terraform struct {
	Span
	RequiredVersion   *Expr
	RequiredProviders []RequiredProvider
	Body              Body
}

// RequiredProvider is one entry of required_providers. Source and Version
// are set only when written as plain strings; Aliases are the
// configuration_aliases as written.
type RequiredProvider struct {
	Name    string
	Source  string
	Version string
	Aliases []string
	Range   hcl.Range // the argument
}

type Provider struct {
	Span
	Name  string
	Alias string // source text of the alias expression
	Body  Body
}

// Resource is a resource or data block, with meta-arguments kept apart from
// the provider attributes and nested blocks. Body holds every argument and
// the nested block tree, meta-arguments included.
type Resource struct {
	Span
	Mode       string // "resource" or "data"
	Type       string
	Name       string
	Meta       []string
	Attributes []string
	Blocks     []string // nested block types; a dynamic block counts as its label
	Body       Body
}

// Moved is a moved block. From and To are the source text; FromAddr and
// ToAddr are the address in canonical form, or "" when the expression is not
// a static address, such as an index with an expression key.
type Moved struct {
	Span
	From, To         string
	FromAddr, ToAddr string
}

type Removed struct {
	Span
	From     string
	FromAddr string
	Body     Body
}

type ModuleCall struct {
	Span
	Name        string
	Source      string
	SourceRange hcl.Range
	Meta        []string
	Attributes  []string
	Body        Body
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
	case KindNoPatch:
		return ReasonNoBase
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
	case KindNoPatch:
		return "a file the diff gives no patch for, so its base content is unknown"
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
	body, tokens, err := parseSyntax(name, src, lim)
	if err != nil {
		return err
	}
	var comments []hclsyntax.Token
	for _, tk := range tokens {
		if tk.Type == hclsyntax.TokenComment {
			comments = append(comments, tk)
		}
	}
	collect(m, name, src, body, comments)
	return nil
}

// parseSyntax parses one file within the limits and returns its body and
// tokens. A panic in the parser is recovered and reported as an error.
func parseSyntax(name string, src []byte, lim Limits) (body *hclsyntax.Body, tokens hclsyntax.Tokens, err error) {
	defer func() {
		if r := recover(); r != nil {
			body, tokens, err = nil, nil, fmt.Errorf("internal error while parsing")
		}
	}()
	tokens, diags := hclsyntax.LexConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("parse error")
	}
	// The parser recurses on nesting and on unary operators, and a stack
	// overflow is fatal, so both are bounded before it runs.
	if len(tokens) > lim.MaxTokens {
		return nil, nil, limitError(fmt.Sprintf("%d tokens over the limit %d", len(tokens), lim.MaxTokens))
	}
	if d := nestingDepth(tokens); d > lim.MaxDepth {
		return nil, nil, limitError(fmt.Sprintf("nesting depth %d over the limit %d", d, lim.MaxDepth))
	}
	if n := prefixRun(tokens); n > lim.MaxPrefixOps {
		return nil, nil, limitError(fmt.Sprintf("%d consecutive unary operators over the limit %d", n, lim.MaxPrefixOps))
	}
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("parse error")
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, nil, fmt.Errorf("parse error: unexpected body type %T", f.Body)
	}
	return body, tokens, nil
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
		sp := spanOf(src, file, line, b.Range())
		switch {
		case b.Type == "variable" && len(b.Labels) == 1:
			v := Variable{Span: sp, Name: b.Labels[0],
				Default: attrExpr(src, b.Body, "default"), Nullable: attrExpr(src, b.Body, "nullable"),
				Sensitive: attrExpr(src, b.Body, "sensitive"), Body: bodyOf(src, b.Body)}
			if a, ok := b.Body.Attributes["type"]; ok {
				v.HasType = true
				v.TypeText = exprText(src, a.Expr)
				v.TypeUsesAny = usesAny(a.Expr)
				v.TypeAttrs = typeAttrs(src, a.Expr, "", nil)
			}
			v.HasDescription = hasText(b.Body.Attributes["description"])
			for _, vb := range b.Body.Blocks {
				if vb.Type == "validation" {
					v.Validations = append(v.Validations, Validation{Condition: attrExpr(src, vb.Body, "condition"),
						ErrorMessage: attrExpr(src, vb.Body, "error_message"), Range: vb.Range()})
				}
			}
			// comments are in source order, so search for the first one in range.
			i := sort.Search(len(comments), func(i int) bool { return comments[i].Range.Start.Line >= line-1 })
			for ; i < len(comments) && comments[i].Range.Start.Line <= b.Body.SrcRange.End.Line; i++ {
				v.Comments = append(v.Comments, Comment{Line: comments[i].Range.Start.Line, Text: strings.TrimRight(string(comments[i].Bytes), "\r\n")})
			}
			m.Variables = append(m.Variables, v)
			m.varNames[v.Name] = true
		case b.Type == "output" && len(b.Labels) == 1:
			o := Output{Span: sp, Name: b.Labels[0],
				HasDescription: hasText(b.Body.Attributes["description"]), Sensitive: attrExpr(src, b.Body, "sensitive"),
				Body: bodyOf(src, b.Body)}
			if a, ok := b.Body.Attributes["value"]; ok {
				o.Value = &Expr{exprText(src, a.Expr), a.Expr.Range()}
				o.References, o.UsesTry = references(a.Expr)
			}
			m.Outputs = append(m.Outputs, o)
			m.outNames[b.Labels[0]] = true
		case b.Type == "locals":
			m.Other = append(m.Other, OtherBlock{Span: sp, Type: b.Type})
			for _, a := range sortedAttrs(b.Body) {
				m.Locals = append(m.Locals, Local{Span: spanOf(src, file, a.SrcRange.Start.Line, a.SrcRange), Name: a.Name,
					Value: Expr{exprText(src, a.Expr), a.Expr.Range()}})
			}
		case b.Type == "provider" && len(b.Labels) == 1:
			p := Provider{Span: sp, Name: b.Labels[0], Body: bodyOf(src, b.Body)}
			if a, ok := b.Body.Attributes["alias"]; ok {
				p.Alias = exprText(src, a.Expr)
			}
			m.Providers = append(m.Providers, p)
		case b.Type == "terraform":
			m.Terraform = append(m.Terraform, terraform(src, b, sp))
			for _, rp := range m.Terraform[len(m.Terraform)-1].RequiredProviders {
				m.RequiredProviders = append(m.RequiredProviders, rp.Name)
			}
		case (b.Type == "resource" || b.Type == "data") && len(b.Labels) == 2:
			r := resource(b, sp)
			r.Body = bodyOf(src, b.Body)
			m.Resources = append(m.Resources, r)
		case b.Type == "moved":
			m.Moved = append(m.Moved, Moved{Span: sp, From: attrText(src, b.Body, "from"), To: attrText(src, b.Body, "to"),
				FromAddr: attrAddr(b.Body, "from"), ToAddr: attrAddr(b.Body, "to")})
		case b.Type == "removed":
			m.Removed = append(m.Removed, Removed{Span: sp, From: attrText(src, b.Body, "from"), FromAddr: attrAddr(b.Body, "from"),
				Body: bodyOf(src, b.Body)})
		case b.Type == "module" && len(b.Labels) == 1:
			c := moduleCall(b, sp)
			c.Body = bodyOf(src, b.Body)
			m.ModuleCalls = append(m.ModuleCalls, c)
		default:
			m.Other = append(m.Other, OtherBlock{Span: sp, Type: b.Type, Labels: b.Labels, Body: bodyOf(src, b.Body)})
		}
	}
	sortStrings(m.RequiredProviders)
}

func terraform(src []byte, b *hclsyntax.Block, sp Span) Terraform {
	t := Terraform{Span: sp, RequiredVersion: attrExpr(src, b.Body, "required_version"), Body: bodyOf(src, b.Body)}
	for _, rb := range b.Body.Blocks {
		if rb.Type != "required_providers" {
			continue
		}
		for _, a := range sortedAttrs(rb.Body) {
			rp := RequiredProvider{Name: a.Name, Range: a.SrcRange}
			// The object form; the legacy string form is a version constraint.
			if obj, ok := a.Expr.(*hclsyntax.ObjectConsExpr); ok {
				for _, it := range obj.Items {
					switch keyName(it.KeyExpr) {
					case "source":
						rp.Source, _ = literalString(it.ValueExpr)
					case "version":
						rp.Version, _ = literalString(it.ValueExpr)
					case "configuration_aliases":
						if tup, ok := it.ValueExpr.(*hclsyntax.TupleConsExpr); ok {
							for _, e := range tup.Exprs {
								rp.Aliases = append(rp.Aliases, exprText(src, e))
							}
						}
					}
				}
			} else {
				rp.Version, _ = literalString(a.Expr)
			}
			t.RequiredProviders = append(t.RequiredProviders, rp)
		}
	}
	return t
}

// bodyOf returns the arguments and nested block tree of a body.
func bodyOf(src []byte, b *hclsyntax.Body) Body {
	var out Body
	for _, a := range sortedAttrs(b) {
		out.Attributes = append(out.Attributes, Attribute{Name: a.Name, Range: a.SrcRange, Value: Expr{exprText(src, a.Expr), a.Expr.Range()}})
	}
	for _, nb := range b.Blocks {
		n := NestedBlock{Type: nb.Type, Labels: nb.Labels, Range: nb.Range()}
		if nb.Type == "dynamic" && len(nb.Labels) == 1 {
			n.Type, n.Labels, n.Dynamic = nb.Labels[0], nil, true
			n.ForEach = attrExpr(src, nb.Body, "for_each")
			if a, ok := nb.Body.Attributes["iterator"]; ok {
				n.Iterator = hcl.ExprAsKeyword(a.Expr)
			}
			for _, cb := range nb.Body.Blocks {
				if cb.Type == "content" {
					n.Body = bodyOf(src, cb.Body)
				}
			}
		} else {
			n.Body = bodyOf(src, nb.Body)
		}
		out.Blocks = append(out.Blocks, n)
	}
	return out
}

// Arg returns the value of the argument name of the body, or nil.
func (b Body) Arg(name string) *Expr {
	for i := range b.Attributes {
		if b.Attributes[i].Name == name {
			return &b.Attributes[i].Value
		}
	}
	return nil
}

// sortedAttrs returns the attributes of a body in source order.
func sortedAttrs(b *hclsyntax.Body) []*hclsyntax.Attribute {
	out := make([]*hclsyntax.Attribute, 0, len(b.Attributes))
	for _, a := range b.Attributes {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SrcRange.Start.Byte < out[j].SrcRange.Start.Byte })
	return out
}

// typeAttrs walks a type constraint and returns its object attributes.
func typeAttrs(src []byte, e hclsyntax.Expression, prefix string, out []TypeAttr) []TypeAttr {
	call, ok := e.(*hclsyntax.FunctionCallExpr)
	if !ok {
		if tup, ok := e.(*hclsyntax.TupleConsExpr); ok {
			for _, x := range tup.Exprs {
				out = typeAttrs(src, x, prefix, out)
			}
		}
		return out
	}
	if call.Name != "object" || len(call.Args) != 1 {
		for _, x := range call.Args {
			out = typeAttrs(src, x, prefix, out)
		}
		return out
	}
	obj, ok := call.Args[0].(*hclsyntax.ObjectConsExpr)
	if !ok {
		return out
	}
	for _, it := range obj.Items {
		name := keyName(it.KeyExpr)
		if name == "" {
			continue
		}
		ta := TypeAttr{Path: prefix + name, Range: hcl.RangeBetween(it.KeyExpr.Range(), it.ValueExpr.Range())}
		inner := it.ValueExpr
		if opt, ok := inner.(*hclsyntax.FunctionCallExpr); ok && opt.Name == "optional" && len(opt.Args) >= 1 {
			ta.Optional, inner = true, opt.Args[0]
			if len(opt.Args) == 2 {
				ta.Default = &Expr{exprText(src, opt.Args[1]), opt.Args[1].Range()}
			}
		}
		out = append(out, ta)
		out = typeAttrs(src, inner, ta.Path+".", out)
	}
	return out
}

// keyName returns an object key written as a name or a plain string.
func keyName(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		e = k.Wrapped
	}
	if kw := hcl.ExprAsKeyword(e); kw != "" {
		return kw
	}
	s, _ := literalString(e)
	return s
}

// references returns the addresses an expression reads and whether it calls
// try().
func references(e hclsyntax.Expression) (refs []string, usesTry bool) {
	for _, t := range e.Variables() {
		if a := refAddress(t); a != "" {
			refs = append(refs, a)
		}
	}
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if c, ok := n.(*hclsyntax.FunctionCallExpr); ok && c.Name == "try" {
			usesTry = true
		}
		return nil
	})
	sortStrings(refs)
	return slices.Compact(refs), usesTry
}

// refAddress is the address a traversal reads: two names for var, local,
// module and resources, three for data, nothing for each, count, path,
// terraform, self and iterator symbols it cannot tell apart from them.
func refAddress(t hcl.Traversal) string {
	names := []string{t.RootName()}
	for _, step := range t[1:] {
		a, ok := step.(hcl.TraverseAttr)
		if !ok {
			break
		}
		names = append(names, a.Name)
	}
	want := 2
	switch names[0] {
	case "each", "count", "path", "terraform", "self":
		return ""
	case "data":
		want = 3
	}
	if len(names) < want {
		return ""
	}
	return strings.Join(names[:want], ".")
}

// attrAddr returns the canonical form of an address argument, or "" when it
// is missing or not a static address.
func attrAddr(body *hclsyntax.Body, name string) string {
	a, ok := body.Attributes[name]
	if !ok {
		return ""
	}
	t, diags := hcl.AbsTraversalForExpr(a.Expr)
	if diags.HasErrors() {
		return ""
	}
	var b strings.Builder
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			b.WriteString(s.Name)
		case hcl.TraverseAttr:
			b.WriteString("." + s.Name)
		case hcl.TraverseIndex:
			switch {
			case !s.Key.IsKnown() || s.Key.IsNull():
				return ""
			case s.Key.Type() == cty.String:
				b.WriteString("[" + strconv.Quote(s.Key.AsString()) + "]")
			case s.Key.Type() == cty.Number:
				b.WriteString("[" + s.Key.AsBigFloat().Text('f', -1) + "]")
			default:
				return ""
			}
		default:
			return ""
		}
	}
	return b.String()
}

func attrExpr(src []byte, body *hclsyntax.Body, name string) *Expr {
	if a, ok := body.Attributes[name]; ok {
		return &Expr{exprText(src, a.Expr), a.Expr.Range()}
	}
	return nil
}

func resource(b *hclsyntax.Block, sp Span) Resource {
	r := Resource{Span: sp, Mode: b.Type, Type: b.Labels[0], Name: b.Labels[1]}
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

func moduleCall(b *hclsyntax.Block, sp Span) ModuleCall {
	c := ModuleCall{Span: sp, Name: b.Labels[0]}
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

// ParseBody lexes and parses one HCL file under the token, nesting and unary
// operator limits, and returns its body and tokens. A panic in the parser is
// recovered and returned as an error. IsLimitError tells a limit apart from a
// parse error.
func ParseBody(name string, src []byte, lim Limits) (*hclsyntax.Body, hclsyntax.Tokens, error) {
	return parseSyntax(name, src, lim)
}

// IsLimitError reports whether err is ParseBody refusing a file over a limit.
func IsLimitError(err error) bool { return errors.As(err, new(limitError)) }
