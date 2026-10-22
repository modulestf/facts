// Package ledger builds the provider field inventory of one module directory
// and checks an author's coverage ledger against it. It reads local files
// only: the module tree and raw `terraform providers schema -json` files.
package ledger

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schema"
)

// Field kinds.
const (
	KindAttribute = "attribute"
	KindBlock     = "block"
)

// Field is one argument, computed attribute or nested block of a resource or
// data source type, at any depth. Name is its path: the names from the type
// down, joined by dots, such as "logging.target_bucket". A nested block or
// nested attribute is a field, and so is each field inside it.
type Field struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Type is the attribute type as Terraform writes it, such as
	// "list of string"; "nested" for a nested attribute type and "block"
	// for a nested block.
	Type       string `json:"type"`
	Required   bool   `json:"required"`
	Optional   bool   `json:"optional"`
	Computed   bool   `json:"computed"`
	Deprecated bool   `json:"deprecated"`
	Sensitive  bool   `json:"sensitive"`
	WriteOnly  bool   `json:"write_only"`
	// Nesting is the nesting mode of a block or nested attribute, "" otherwise.
	Nesting string `json:"nesting"`
	// MinItems and MaxItems are the schema's bounds of a block or nested
	// attribute, 0 when it states none; nil for any other field.
	MinItems *uint64 `json:"min_items,omitempty"`
	MaxItems *uint64 `json:"max_items,omitempty"`
}

// maxDepth caps how deep fields nest, so a hostile schema file cannot make
// the walk recurse without bound.
const maxDepth = 32

// Type is the inventory of one resource or data source type the module uses.
type Type struct {
	Mode     string  `json:"mode"` // "resource" or "data"
	Type     string  `json:"type"`
	Provider string  `json:"provider"` // the provider source address in the schema file
	Fields   []Field `json:"fields"`
}

// Inventory is the field inventory of one module directory, with what the
// ledger check needs from the code.
type Inventory struct {
	Types       []Type
	SchemaFiles []schema.FileHash
	variables   map[string]bool
	outputs     map[string]bool
}

// Build reads the module in dir, not its submodules or examples, and lists
// the fields of every resource and data source type it declares, from the
// raw schema files. A module with a file the parser cannot model, a provider
// with no schema, a type the schema lacks, an ephemeral block, or a
// directory with no .tf file or no resource or data block is an error: an inventory with a
// gap would let a ledger pass that misses fields.
func Build(dir string, schemaFiles []string, lim facts.Limits) (*Inventory, error) {
	if len(schemaFiles) == 0 {
		return nil, errors.New("at least one --schema-files file is needed")
	}
	set, err := schema.Load("", schemaFiles)
	if err != nil {
		return nil, err
	}
	tree, err := facts.ReadTree(dir, lim)
	if err != nil {
		return nil, fmt.Errorf("module tree: %w", err)
	}
	m, err := facts.ParseDir(tree, ".", lim)
	if err != nil {
		return nil, fmt.Errorf("module tree: %w", err)
	}
	if len(m.Unsupported) > 0 {
		return nil, fmt.Errorf("module tree: %s", strings.Join(m.Unsupported, "; "))
	}
	if !slices.ContainsFunc(tree.DirEntries("."), func(e *facts.Entry) bool { return strings.HasSuffix(e.Path, ".tf") }) {
		return nil, errors.New("module tree: no .tf file in the module directory")
	}
	for _, b := range m.Other {
		if b.Type == "ephemeral" {
			// shortcut: ephemeral resource schemas are not read; upgrade when the schema package reads them.
			return nil, fmt.Errorf("%s:%d: ephemeral resources are not inventoried", b.File, b.Line)
		}
	}
	if len(m.Resources) == 0 {
		return nil, errors.New("module tree: no resource or data block, so there is nothing to inventory")
	}
	inv := &Inventory{Types: []Type{}, SchemaFiles: set.SchemaFiles, variables: map[string]bool{}, outputs: map[string]bool{}}
	for _, v := range m.Variables {
		inv.variables[v.Name] = true
	}
	for _, o := range m.Outputs {
		inv.outputs[o.Name] = true
	}
	seen := map[string]bool{}
	for _, r := range m.Resources {
		if seen[r.Mode+" "+r.Type] {
			continue
		}
		seen[r.Mode+" "+r.Type] = true
		t, err := typeInventory(set, m, r)
		if err != nil {
			return nil, err
		}
		inv.Types = append(inv.Types, t)
	}
	slices.SortFunc(inv.Types, func(a, b Type) int { return cmp.Or(cmp.Compare(a.Mode, b.Mode), cmp.Compare(a.Type, b.Type)) })
	return inv, nil
}

func typeInventory(set *schema.Set, m *facts.Module, r facts.Resource) (Type, error) {
	source := facts.ResourceProviderSource(m, r)
	out, f := set.Lookup(schema.Query{Source: source, DataSource: r.Mode == "data", Type: r.Type})
	switch out {
	case schema.Unavailable:
		return Type{}, fmt.Errorf("%s %s: no schema file has provider %s", r.Mode, r.Type, source)
	case schema.AbsentFromSchema:
		return Type{}, fmt.Errorf("%s %s: provider %s has no such type", r.Mode, r.Type, source)
	}
	fs, err := blockFields(nil, "", f.Block, 0)
	if err != nil {
		return Type{}, fmt.Errorf("%s %s: %w", r.Mode, r.Type, err)
	}
	slices.SortFunc(fs, func(a, b Field) int { return cmp.Compare(a.Name, b.Name) })
	return Type{Mode: r.Mode, Type: r.Type, Provider: source, Fields: fs}, nil
}

// blockFields appends the fields of b under prefix, and every field inside them.
func blockFields(out []Field, prefix string, b *tfjson.SchemaBlock, depth int) ([]Field, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("fields nest deeper than %d", maxDepth)
	}
	out, err := attributeFields(out, prefix, b.Attributes, depth)
	if err != nil {
		return nil, err
	}
	for name, bt := range b.NestedBlocks {
		minItems, maxItems := bt.MinItems, bt.MaxItems
		f := Field{Name: prefix + name, Kind: KindBlock, Type: "block", Nesting: string(bt.NestingMode),
			Required: minItems > 0, Optional: minItems == 0, MinItems: &minItems, MaxItems: &maxItems}
		if bt.Block != nil {
			f.Deprecated = bt.Block.Deprecated
		}
		out = append(out, f)
		if bt.Block != nil {
			if out, err = blockFields(out, f.Name+".", bt.Block, depth+1); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func attributeFields(out []Field, prefix string, attrs map[string]*tfjson.SchemaAttribute, depth int) ([]Field, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("fields nest deeper than %d", maxDepth)
	}
	for name, a := range attrs {
		f := Field{Name: prefix + name, Kind: KindAttribute, Required: a.Required, Optional: a.Optional, Computed: a.Computed,
			Deprecated: a.Deprecated, Sensitive: a.Sensitive, WriteOnly: a.WriteOnly}
		nt := a.AttributeNestedType
		switch {
		case nt != nil:
			minItems, maxItems := nt.MinItems, nt.MaxItems
			f.Type, f.Nesting, f.MinItems, f.MaxItems = "nested", string(nt.NestingMode), &minItems, &maxItems
		case a.AttributeType != cty.NilType:
			f.Type = a.AttributeType.FriendlyName()
		}
		out = append(out, f)
		if nt != nil {
			var err error
			if out, err = attributeFields(out, f.Name+".", nt.Attributes, depth+1); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
