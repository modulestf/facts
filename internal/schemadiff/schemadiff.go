// Package schemadiff compares two `terraform providers schema -json` files
// for the resource and data source types a module tree uses: the arguments,
// attributes and nested blocks added, removed, deprecated or newly required
// between the two, and every type missing in either. It reads local files
// only and never fetches.
package schemadiff

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schema"
)

// SchemaVersion is the version of the report layout in
// schema/schema-diff.schema.json.
const SchemaVersion = 1

// Change kinds.
const (
	Added         = "added"          // in the to schema only
	Removed       = "removed"        // in the from schema only
	Deprecated    = "deprecated"     // deprecated in to, not in from
	NewlyRequired = "newly_required" // required in to, not in from (absent counts as not required)
	Absent        = "absent"         // a type in neither schema
)

// Element kinds.
const (
	ElementType      = "type"      // the resource or data source type itself
	ElementArgument  = "argument"  // a required or optional attribute
	ElementAttribute = "attribute" // a computed-only attribute
	ElementBlock     = "block"     // a nested block
)

// Report is the output of one schema-diff run.
type Report struct {
	SchemaVersion int     `json:"schema_version"`
	Tool          Tool    `json:"tool"`
	From          File    `json:"from"`
	To            File    `json:"to"`
	Types         []Type  `json:"types"`
	Changes       []Entry `json:"changes"`
}

// Tool names the binary that wrote the report.
type Tool struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// File is one schema file by base name, so no host path is recorded.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Type is a resource or data source type the module tree uses, with the
// provider source it resolves to.
type Type struct {
	Mode           string `json:"mode"` // "resource" or "data"
	Type           string `json:"type"`
	ProviderSource string `json:"provider_source"`
}

// Entry is one change. Path is empty for the type itself.
type Entry struct {
	Type
	Path    []string `json:"path"`
	Element string   `json:"element"`
	Change  string   `json:"change"`
}

// Options is the input of Run.
type Options struct {
	From, To  string // schema files
	TypesFrom string // module tree
	Limits    facts.Limits
	Tool      Tool
}

// Run reads both schema files and the module tree and returns the report.
// A directory whose .tf files cannot all be parsed is an error: the type
// list would be incomplete.
func Run(o Options) (*Report, error) {
	from, err := load(o.From)
	if err != nil {
		return nil, fmt.Errorf("--from: %w", err)
	}
	to, err := load(o.To)
	if err != nil {
		return nil, fmt.Errorf("--to: %w", err)
	}
	types, err := ModuleTypes(o.TypesFrom, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("--types-from: %w", err)
	}
	r := &Report{
		SchemaVersion: SchemaVersion,
		Tool:          o.Tool,
		From:          File(from.SchemaFiles[0]),
		To:            File(to.SchemaFiles[0]),
		Types:         types,
		Changes:       []Entry{},
	}
	for _, t := range types {
		r.Changes = append(r.Changes, Diff(t, block(from, t), block(to, t))...)
	}
	return r, nil
}

func load(p string) (*schema.Set, error) {
	s, err := schema.Load("", []string{p})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// block returns the type's schema block, nil when the file lacks the
// provider or the type.
func block(s *schema.Set, t Type) *tfjson.SchemaBlock {
	o, f := s.Lookup(schema.Query{Source: t.ProviderSource, DataSource: t.Mode == "data", Type: t.Type})
	if o != schema.Present {
		return nil
	}
	return f.Block
}

// ModuleTypes lists the resource and data source types every directory of
// the tree at dir uses, sorted by mode, type and provider source.
func ModuleTypes(dir string, lim facts.Limits) ([]Type, error) {
	t, err := facts.ReadTree(dir, lim)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(t.Dirs))
	for d := range t.Dirs {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	seen := map[Type]bool{}
	types := []Type{}
	for _, d := range dirs {
		m, err := facts.ParseDir(t, d, lim)
		if err != nil {
			return nil, err
		}
		if len(m.Unsupported) > 0 {
			return nil, fmt.Errorf("the types of %s cannot be listed: %s", d, m.Unsupported[0])
		}
		for _, r := range m.Resources {
			ty := Type{Mode: r.Mode, Type: r.Type, ProviderSource: resolveSource(m, r)}
			if !seen[ty] {
				seen[ty] = true
				types = append(types, ty)
			}
		}
	}
	slices.SortFunc(types, func(a, b Type) int {
		return strings.Compare(a.Mode+" "+a.Type+" "+a.ProviderSource, b.Mode+" "+b.Type+" "+b.ProviderSource)
	})
	return types, nil
}

// resolveSource is the provider source of r: the local name from its
// provider argument or its type's prefix, looked up in the directory's
// required_providers, else the implied hashicorp namespace.
func resolveSource(m *facts.Module, r facts.Resource) string {
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	for _, tf := range m.Terraform {
		for _, rp := range tf.RequiredProviders {
			if rp.Name == local && rp.Source != "" {
				return providerKey(rp.Source)
			}
		}
	}
	return providerKey(local)
}

// providerKey normalizes a provider source address the way the schema file
// keys it: lower case, with the default registry host and namespace filled
// in. The facts package applies the same rule to version floors.
func providerKey(source string) string {
	s := strings.ToLower(source)
	switch strings.Count(s, "/") {
	case 0:
		return "registry.terraform.io/hashicorp/" + s
	case 1:
		return "registry.terraform.io/" + s
	}
	return s
}

// Diff compares the from and to blocks of one type. A nil block is a type
// missing from that schema.
func Diff(t Type, from, to *tfjson.SchemaBlock) []Entry {
	d := differ{t: t, out: []Entry{}}
	switch {
	case from == nil && to == nil:
		d.add(nil, ElementType, Absent)
	case from == nil:
		d.add(nil, ElementType, Added)
	case to == nil:
		d.add(nil, ElementType, Removed)
	default:
		if to.Deprecated && !from.Deprecated {
			d.add(nil, ElementType, Deprecated)
		}
		d.block(nil, from, to)
	}
	return d.out
}

type differ struct {
	t   Type
	out []Entry
}

func (d *differ) add(path []string, element, change string) {
	d.out = append(d.out, Entry{Type: d.t, Path: append([]string{}, path...), Element: element, Change: change})
}

// block compares the attributes, then the nested blocks, of two blocks.
// An added or removed block is one row: its contents are not listed.
func (d *differ) block(path []string, from, to *tfjson.SchemaBlock) {
	d.attributes(path, from.Attributes, to.Attributes)
	for _, name := range union(from.NestedBlocks, to.NestedBlocks) {
		p := append(slices.Clone(path), name)
		f, t := nestedBlock(from, name), nestedBlock(to, name)
		switch {
		case f == nil && t == nil:
		case f == nil:
			d.add(p, ElementBlock, Added)
			if t.MinItems > 0 {
				d.add(p, ElementBlock, NewlyRequired)
			}
		case t == nil:
			d.add(p, ElementBlock, Removed)
		default:
			if t.Block.Deprecated && !f.Block.Deprecated {
				d.add(p, ElementBlock, Deprecated)
			}
			if t.MinItems > 0 && f.MinItems == 0 {
				d.add(p, ElementBlock, NewlyRequired)
			}
			d.block(p, f.Block, t.Block)
		}
	}
}

func (d *differ) attributes(path []string, from, to map[string]*tfjson.SchemaAttribute) {
	for _, name := range union(from, to) {
		p := append(slices.Clone(path), name)
		f, t := from[name], to[name]
		switch {
		case f == nil:
			d.add(p, element(t), Added)
			if t.Required {
				d.add(p, element(t), NewlyRequired)
			}
		case t == nil:
			d.add(p, element(f), Removed)
		default:
			if t.Deprecated && !f.Deprecated {
				d.add(p, element(t), Deprecated)
			}
			if t.Required && !f.Required {
				d.add(p, element(t), NewlyRequired)
			}
			if f.AttributeNestedType != nil && t.AttributeNestedType != nil {
				d.attributes(p, f.AttributeNestedType.Attributes, t.AttributeNestedType.Attributes)
			}
		}
	}
}

// nestedBlock returns b's nested block name, nil when it has none or the
// file gives it no block.
func nestedBlock(b *tfjson.SchemaBlock, name string) *tfjson.SchemaBlockType {
	if bt := b.NestedBlocks[name]; bt != nil && bt.Block != nil {
		return bt
	}
	return nil
}

func element(a *tfjson.SchemaAttribute) string {
	if a.Required || a.Optional {
		return ElementArgument
	}
	return ElementAttribute
}

// union returns the keys of a and b with a non-nil value, sorted.
func union[V any](a, b map[string]*V) []string {
	set := map[string]bool{}
	for _, m := range []map[string]*V{a, b} {
		for k, v := range m {
			if v != nil {
				set[k] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}
