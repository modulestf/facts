package facts

import (
	"fmt"
	"path"
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

	Variables         []Variable
	Outputs           []Output
	RequiredProviders []string
	Providers         []Provider
	Resources         []Resource
	Moved             []Moved
	Removed           []Removed
	ModuleCalls       []ModuleCall
}

type Variable struct {
	Name           string
	File           string
	Line           int
	HasType        bool
	TypeUsesAny    bool // the type expression names `any` anywhere
	Commented      bool // a comment sits inside the block or on the line above it
	HasDescription bool // present and not an empty string literal
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

// ParseDir parses the *.tf files directly in dir.
func ParseDir(t *Tree, dir string, lim Limits) *Module {
	m := &Module{Dir: dir}
	for _, e := range t.DirEntries(dir) {
		name := path.Base(e.Path)
		if r := unsupportedName(name); r != "" {
			m.Unsupported = append(m.Unsupported, r)
			continue
		}
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		if e.Kind != KindFile {
			m.Unsupported = append(m.Unsupported, fmt.Sprintf("%s is %s", name, kindText(e.Kind)))
			continue
		}
		if err := parseFile(m, name, e.Data, lim); err != nil {
			m.Unsupported = append(m.Unsupported, fmt.Sprintf("%s: %v", name, err))
		}
	}
	return m
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
	if d := nestingDepth(tokens); d > lim.MaxDepth {
		return fmt.Errorf("nesting depth %d over the limit %d", d, lim.MaxDepth)
	}
	f, diags := hclsyntax.ParseConfig(src, name, hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("parse error")
	}
	var commentLines []int
	for _, tk := range tokens {
		if tk.Type == hclsyntax.TokenComment {
			commentLines = append(commentLines, tk.Range.Start.Line)
		}
	}
	collect(m, name, src, f.Body.(*hclsyntax.Body), commentLines)
	return nil
}

func nestingDepth(tokens hclsyntax.Tokens) int {
	depth, max := 0, 0
	for _, tk := range tokens {
		switch tk.Type {
		case hclsyntax.TokenOBrace, hclsyntax.TokenOBrack, hclsyntax.TokenOParen,
			hclsyntax.TokenTemplateInterp, hclsyntax.TokenTemplateControl, hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			depth++
			if depth > max {
				max = depth
			}
		case hclsyntax.TokenCBrace, hclsyntax.TokenCBrack, hclsyntax.TokenCParen,
			hclsyntax.TokenTemplateSeqEnd, hclsyntax.TokenCQuote, hclsyntax.TokenCHeredoc:
			depth--
		}
	}
	return max
}

func collect(m *Module, file string, src []byte, body *hclsyntax.Body, commentLines []int) {
	for _, b := range body.Blocks {
		line := b.TypeRange.Start.Line
		switch {
		case b.Type == "variable" && len(b.Labels) == 1:
			v := Variable{Name: b.Labels[0], File: file, Line: line}
			if a, ok := b.Body.Attributes["type"]; ok {
				v.HasType = true
				v.TypeUsesAny = usesAny(a.Expr)
			}
			v.HasDescription = hasText(b.Body.Attributes["description"])
			for _, l := range commentLines {
				if l >= line-1 && l <= b.Body.SrcRange.End.Line {
					v.Commented = true
				}
			}
			m.Variables = append(m.Variables, v)
		case b.Type == "output" && len(b.Labels) == 1:
			m.Outputs = append(m.Outputs, Output{Name: b.Labels[0], File: file, Line: line,
				HasDescription: hasText(b.Body.Attributes["description"])})
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
			if v, diags := a.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
				c.Source = v.AsString()
			}
		}
	}
	sortStrings(c.Meta, c.Attributes)
	return c
}

// usesAny reports whether a type expression names the `any` keyword.
func usesAny(e hclsyntax.Expression) bool {
	found := false
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok && len(st.Traversal) == 1 && st.Traversal.RootName() == "any" {
			found = true
		}
		return nil
	})
	return found
}

// hasText reports whether an attribute is present and not an empty or
// blank string literal. A non-literal expression counts as text.
func hasText(a *hclsyntax.Attribute) bool {
	if a == nil {
		return false
	}
	v, diags := a.Expr.Value(nil)
	switch {
	case diags.HasErrors() || !v.IsKnown():
		return true // an expression with references
	case v.IsNull():
		return false
	case v.Type() != cty.String:
		return true
	}
	return strings.TrimSpace(v.AsString()) != ""
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
