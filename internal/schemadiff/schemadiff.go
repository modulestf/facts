// Package schemadiff compares two `terraform providers schema -json` files
// for the resource and data source types a module tree uses: the arguments,
// attributes and nested blocks added, removed, deprecated, newly required or
// changed in a way that breaks callers, and every type missing in either
// file. It reads local files only and never fetches.
package schemadiff

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schema"
)

// SchemaVersion is the version of the report layout in
// schema/schema-diff.schema.json.
const SchemaVersion = 1

// Change kinds.
const (
	Added            = "added"              // in the to schema only
	Removed          = "removed"            // in the from schema only
	Deprecated       = "deprecated"         // deprecated in to, not in from
	NewlyRequired    = "newly_required"     // required in to, not in from (absent counts as not required)
	TypeChanged      = "type_changed"       // an attribute's type, or its nested or plain form, changed
	NoLongerSettable = "no_longer_settable" // an argument became computed-only
	NestingChanged   = "nesting_changed"    // a block's nesting_mode, min_items or max_items changed
	SensitiveChanged = "sensitive_changed"  // an attribute's sensitive flag changed
	WriteOnlyChanged = "write_only_changed" // an attribute's write_only flag changed
	Absent           = "absent"             // a type in neither file, whose provider both have
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
	SchemaVersion int       `json:"schema_version"`
	Tool          Tool      `json:"tool"`
	From          File      `json:"from"`
	To            File      `json:"to"`
	Types         []TypeUse `json:"types"`
	Changes       []Entry   `json:"changes"`
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

// Type is a resource or data source type with the provider source it
// resolves to.
type Type struct {
	Mode           string `json:"mode"` // "resource" or "data"
	Type           string `json:"type"`
	ProviderSource string `json:"provider_source"`
}

// TypeUse is a type the module tree uses and the directories, relative to
// the tree, that use it.
type TypeUse struct {
	Type
	Dirs []string `json:"dirs"`
}

// Entry is one change. Path is empty for the type itself. From and To
// describe the element in each file, nil where it is absent.
type Entry struct {
	Type
	Path    []string `json:"path"`
	Element string   `json:"element"`
	Change  string   `json:"change"`
	From    *Value   `json:"from"`
	To      *Value   `json:"to"`
}

// Value is what one file says of an element. The fields set depend on the
// element: Deprecated for every element; the attribute fields for an
// argument or attribute; the block fields for a block.
type Value struct {
	Deprecated bool `json:"deprecated"`
	// An attribute has either AttributeType, its type as written in the
	// schema file, or NestedType, the nesting mode of its nested type.
	AttributeType any     `json:"attribute_type,omitempty"`
	NestedType    string  `json:"nested_type,omitempty"`
	Required      *bool   `json:"required,omitempty"`
	Optional      *bool   `json:"optional,omitempty"`
	Computed      *bool   `json:"computed,omitempty"`
	Sensitive     *bool   `json:"sensitive,omitempty"`
	WriteOnly     *bool   `json:"write_only,omitempty"`
	NestingMode   string  `json:"nesting_mode,omitempty"`
	MinItems      *uint64 `json:"min_items,omitempty"`
	MaxItems      *uint64 `json:"max_items,omitempty"`
}

// Options is the input of Run.
type Options struct {
	From, To  string // schema files
	TypesFrom string // module tree
	Limits    facts.Limits
	Tool      Tool
}

// Run reads both schema files and the module tree and returns the report.
// It is an error when a directory's .tf files cannot all be parsed, when a
// directory has an ephemeral block, or when a provider the tree uses is
// missing from either file: the report would be incomplete or wrong.
func Run(o Options) (*Report, error) {
	from, err := load("--from", o.From)
	if err != nil {
		return nil, err
	}
	to, err := load("--to", o.To)
	if err != nil {
		return nil, err
	}
	types, err := ModuleTypes(o.TypesFrom, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("--types-from: %w", baseNamed(err, o.TypesFrom))
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
		fb, err := block(from, r.From.Path, t.Type)
		if err != nil {
			return nil, err
		}
		tb, err := block(to, r.To.Path, t.Type)
		if err != nil {
			return nil, err
		}
		r.Changes = append(r.Changes, Diff(t.Type, fb, tb)...)
	}
	return r, nil
}

// load reads one schema file. Its errors name the file by base name only.
func load(flag, p string) (*schema.Set, error) {
	s, err := schema.Load("", []string{p})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, baseNamed(err, p))
	}
	return s, nil
}

// baseNamed returns err with every occurrence of the path p replaced by its
// base name, so no host path is printed.
func baseNamed(err error, p string) error {
	if p == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), p, filepath.Base(p)))
}

// block returns the type's schema block, nil when the file has the provider
// but not the type. A provider the file lacks is an error.
func block(s *schema.Set, name string, t Type) (*tfjson.SchemaBlock, error) {
	o, f := s.Lookup(schema.Query{Source: t.ProviderSource, DataSource: t.Mode == "data", Type: t.Type})
	switch o {
	case schema.Present:
		return f.Block, nil
	case schema.AbsentFromSchema:
		return nil, nil
	}
	return nil, fmt.Errorf("provider %s, which %s %s uses, is not in %s", t.ProviderSource, t.Mode, t.Type, name)
}

// ModuleTypes lists the resource and data source types every directory of
// the tree at dir uses, sorted by mode, type and provider source, each with
// the sorted directories that use it.
func ModuleTypes(dir string, lim facts.Limits) ([]TypeUse, error) {
	t, err := facts.ReadTree(dir, lim)
	if err != nil {
		return nil, err
	}
	dirs := slices.Sorted(maps.Keys(t.Dirs))
	uses := map[Type][]string{}
	for _, d := range dirs {
		m, err := facts.ParseDir(t, d, lim)
		if err != nil {
			return nil, err
		}
		if len(m.Unsupported) > 0 {
			return nil, fmt.Errorf("the types of %s cannot be listed: %s", d, m.Unsupported[0])
		}
		for _, b := range m.Other {
			if b.Type == "ephemeral" {
				// shortcut: ephemeral resource schemas are not compared; upgrade when the schema package reads them.
				return nil, fmt.Errorf("%s:%d: ephemeral resources are not compared", path.Join(d, b.File), b.Line)
			}
		}
		for _, r := range m.Resources {
			ty := Type{Mode: r.Mode, Type: r.Type, ProviderSource: facts.ResourceProviderSource(m, r)}
			if !slices.Contains(uses[ty], d) {
				uses[ty] = append(uses[ty], d)
			}
		}
	}
	out := make([]TypeUse, 0, len(uses))
	for ty, ds := range uses {
		out = append(out, TypeUse{Type: ty, Dirs: ds})
	}
	slices.SortFunc(out, func(a, b TypeUse) int {
		return strings.Compare(a.Mode+" "+a.Type.Type+" "+a.ProviderSource, b.Mode+" "+b.Type.Type+" "+b.ProviderSource)
	})
	return out, nil
}

// Diff compares the from and to blocks of one type. A nil block is a type
// missing from that file.
func Diff(t Type, from, to *tfjson.SchemaBlock) []Entry {
	d := differ{t: t, out: []Entry{}}
	tv := func(b *tfjson.SchemaBlock) *Value {
		if b == nil {
			return nil
		}
		return &Value{Deprecated: b.Deprecated}
	}
	fv, toV := tv(from), tv(to)
	switch {
	case from == nil && to == nil:
		d.add(nil, ElementType, Absent, fv, toV)
	case from == nil:
		d.add(nil, ElementType, Added, fv, toV)
	case to == nil:
		d.add(nil, ElementType, Removed, fv, toV)
	default:
		if to.Deprecated && !from.Deprecated {
			d.add(nil, ElementType, Deprecated, fv, toV)
		}
		d.block(nil, from, to)
	}
	return d.out
}

type differ struct {
	t   Type
	out []Entry
}

func (d *differ) add(path []string, element, change string, from, to *Value) {
	d.out = append(d.out, Entry{Type: d.t, Path: append([]string{}, path...), Element: element, Change: change, From: from, To: to})
}

// block compares the attributes, then the nested blocks, of two blocks.
// An added or removed block is one row: its contents are not listed.
func (d *differ) block(path []string, from, to *tfjson.SchemaBlock) {
	d.attributes(path, from.Attributes, to.Attributes)
	for _, name := range union(from.NestedBlocks, to.NestedBlocks) {
		p := append(slices.Clone(path), name)
		f, t := nestedBlock(from, name), nestedBlock(to, name)
		fv, tv := blockValue(f), blockValue(t)
		switch {
		case f == nil && t == nil:
		case f == nil:
			d.add(p, ElementBlock, Added, fv, tv)
			if t.MinItems > 0 {
				d.add(p, ElementBlock, NewlyRequired, fv, tv)
			}
		case t == nil:
			d.add(p, ElementBlock, Removed, fv, tv)
		default:
			if t.Block.Deprecated && !f.Block.Deprecated {
				d.add(p, ElementBlock, Deprecated, fv, tv)
			}
			if t.MinItems > 0 && f.MinItems == 0 {
				d.add(p, ElementBlock, NewlyRequired, fv, tv)
			}
			if f.NestingMode != t.NestingMode || f.MinItems != t.MinItems || f.MaxItems != t.MaxItems {
				d.add(p, ElementBlock, NestingChanged, fv, tv)
			}
			d.block(p, f.Block, t.Block)
		}
	}
}

func (d *differ) attributes(path []string, from, to map[string]*tfjson.SchemaAttribute) {
	for _, name := range union(from, to) {
		p := append(slices.Clone(path), name)
		f, t := from[name], to[name]
		fv, tv := attributeValue(f), attributeValue(t)
		switch {
		case f == nil:
			d.add(p, element(t), Added, fv, tv)
			if t.Required {
				d.add(p, element(t), NewlyRequired, fv, tv)
			}
		case t == nil:
			d.add(p, element(f), Removed, fv, tv)
		default:
			el := element(t)
			if t.Deprecated && !f.Deprecated {
				d.add(p, el, Deprecated, fv, tv)
			}
			if t.Required && !f.Required {
				d.add(p, el, NewlyRequired, fv, tv)
			}
			if element(f) == ElementArgument && el == ElementAttribute {
				d.add(p, el, NoLongerSettable, fv, tv)
			}
			if !sameType(f, t) {
				d.add(p, el, TypeChanged, fv, tv)
			}
			if f.Sensitive != t.Sensitive {
				d.add(p, el, SensitiveChanged, fv, tv)
			}
			if f.WriteOnly != t.WriteOnly {
				d.add(p, el, WriteOnlyChanged, fv, tv)
			}
			if f.AttributeNestedType != nil && t.AttributeNestedType != nil {
				d.attributes(p, f.AttributeNestedType.Attributes, t.AttributeNestedType.Attributes)
			}
		}
	}
}

// sameType reports whether two attributes have the same plain type, or are
// both nested with the same nesting mode. The nested attributes themselves
// are compared one by one.
func sameType(f, t *tfjson.SchemaAttribute) bool {
	fn, tn := f.AttributeNestedType, t.AttributeNestedType
	switch {
	case fn != nil && tn != nil:
		return fn.NestingMode == tn.NestingMode
	case fn != nil || tn != nil:
		return false
	}
	return f.AttributeType.Equals(t.AttributeType)
}

func attributeValue(a *tfjson.SchemaAttribute) *Value {
	if a == nil {
		return nil
	}
	v := &Value{
		Deprecated: a.Deprecated,
		Required:   new(a.Required),
		Optional:   new(a.Optional),
		Computed:   new(a.Computed),
		Sensitive:  new(a.Sensitive),
		WriteOnly:  new(a.WriteOnly),
	}
	if a.AttributeNestedType != nil {
		v.NestedType = string(a.AttributeNestedType.NestingMode)
	} else if a.AttributeType != cty.NilType {
		v.AttributeType = a.AttributeType
	}
	return v
}

func blockValue(b *tfjson.SchemaBlockType) *Value {
	if b == nil {
		return nil
	}
	return &Value{
		Deprecated:  b.Block.Deprecated,
		NestingMode: string(b.NestingMode),
		MinItems:    new(b.MinItems),
		MaxItems:    new(b.MaxItems),
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
