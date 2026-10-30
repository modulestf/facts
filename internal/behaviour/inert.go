package behaviour

import (
	"path"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// scan holds the read sites of both trees, and whether every .tf file of
// both was scanned.
type scan struct {
	sites    []facts.ReadSite
	complete bool
	roots    []string // the directories a root-anchored path may be relative to
}

// scanReadSites scans every .tf file of the trees with facts.ReadSites.
func scanReadSites(trees []*facts.Tree, lim facts.Limits) (*scan, error) {
	s := &scan{complete: true, roots: []string{"."}}
	seen := map[string]bool{}
	for _, t := range trees {
		sites, complete, err := facts.ReadSites(t, lim)
		if err != nil {
			return nil, err
		}
		s.complete = s.complete && complete
		s.sites = append(s.sites, sites...)
		for _, p := range t.Paths {
			// An example directory is where Terraform runs, so a path
			// relative to the working directory may be relative to it.
			if parts := strings.Split(p, "/"); len(parts) == 3 && parts[0] == "examples" && isConfigName(p) {
				if d := path.Dir(p); !seen[d] {
					seen[d] = true
					s.roots = append(s.roots, d)
				}
			}
		}
	}
	return s, nil
}

// reachedBy returns the first read site that may read p, if any.
func (s *scan) reachedBy(p string) (facts.ReadSite, bool) {
	for _, site := range s.sites {
		if site.Reaches(p, s.roots) {
			return site, true
		}
	}
	return facts.ReadSite{}, false
}

// templatePart is one part of a template: literal text, or a path.module,
// path.root or path.cwd reference (pathRef "module", "root", "cwd"), or
// anything else (neither set).
type templatePart struct {
	literal bool
	text    string
	pathRef string
}

// templateParts splits a string expression into its parts. ok is false for
// an expression that is not a string literal or a template.
func templateParts(e hclsyntax.Expression) ([]templatePart, bool) {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		if s, ok := literalString(x); ok {
			return []templatePart{{literal: true, text: s}}, true
		}
		return nil, false
	case *hclsyntax.TemplateWrapExpr:
		return []templatePart{refPart(x.Wrapped)}, true
	case *hclsyntax.TemplateExpr:
		var out []templatePart
		for _, p := range x.Parts {
			if lit, ok := p.(*hclsyntax.LiteralValueExpr); ok {
				if s, ok := literalString(lit); ok {
					out = append(out, templatePart{literal: true, text: s})
					continue
				}
			}
			out = append(out, refPart(p))
		}
		return out, true
	case *hclsyntax.ParenthesesExpr:
		return templateParts(x.Expression)
	}
	return nil, false
}

func refPart(e hclsyntax.Expression) templatePart {
	st, ok := e.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(st.Traversal) != 2 || st.Traversal.RootName() != "path" {
		return templatePart{}
	}
	if name := attrName(st.Traversal[1]); name == "module" || name == "root" || name == "cwd" {
		return templatePart{pathRef: name}
	}
	return templatePart{}
}

// literalPrefix joins the literal parts up to the first other part. static
// is set when every part is literal.
func literalPrefix(parts []templatePart) (string, bool) {
	var b strings.Builder
	for _, p := range parts {
		if !p.literal {
			return b.String(), false
		}
		b.WriteString(p.text)
	}
	return b.String(), true
}

// ignoredName reports a file Terraform itself skips: hidden files and
// editor backup and lock files.
func ignoredName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") ||
		(strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#"))
}
