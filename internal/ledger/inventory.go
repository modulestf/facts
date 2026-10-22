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

// Field is one top-level argument, computed attribute or nested block of a
// resource or data source type. A nested block or nested attribute is one
// field: its content goes with the variable that exposes it.
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
}

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
// with no schema or a type the schema lacks is an error: an inventory with a
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
	areas, err := facts.FindAreas(tree, nil, lim, false)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(areas, func(a *facts.Area) bool { return a.Path == "." })
	if i < 0 || !areas[i].HeadExists {
		return nil, errors.New("module tree: no root module")
	}
	m := areas[i].Head
	if areas[i].Issue != "" {
		return nil, fmt.Errorf("module tree: %s", areas[i].Issue)
	}
	if len(m.Unsupported) > 0 {
		return nil, fmt.Errorf("module tree: %s", strings.Join(m.Unsupported, "; "))
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
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	source := providerSource(m, local)
	out, f := set.Lookup(schema.Query{Source: source, DataSource: r.Mode == "data", Type: r.Type})
	switch out {
	case schema.Unavailable:
		return Type{}, fmt.Errorf("%s %s: no schema file has provider %s", r.Mode, r.Type, source)
	case schema.AbsentFromSchema:
		return Type{}, fmt.Errorf("%s %s: provider %s has no such type", r.Mode, r.Type, source)
	}
	return Type{Mode: r.Mode, Type: r.Type, Provider: source, Fields: fields(f.Block)}, nil
}

// providerSource is the schema file key of the provider a module names by
// local: its required_providers source, or hashicorp/<local> as Terraform
// assumes, with the default registry host added to a two-part address.
func providerSource(m *facts.Module, local string) string {
	src := "hashicorp/" + local
	for _, t := range m.Terraform {
		for _, rp := range t.RequiredProviders {
			if rp.Name == local && rp.Source != "" {
				src = rp.Source
			}
		}
	}
	src = strings.ToLower(src)
	if strings.Count(src, "/") == 1 {
		src = "registry.terraform.io/" + src
	}
	return src
}

func fields(b *tfjson.SchemaBlock) []Field {
	out := []Field{}
	for name, a := range b.Attributes {
		f := Field{Name: name, Kind: KindAttribute, Required: a.Required, Optional: a.Optional, Computed: a.Computed,
			Deprecated: a.Deprecated, Sensitive: a.Sensitive, WriteOnly: a.WriteOnly}
		switch {
		case a.AttributeNestedType != nil:
			f.Type, f.Nesting = "nested", string(a.AttributeNestedType.NestingMode)
		case a.AttributeType != cty.NilType:
			f.Type = a.AttributeType.FriendlyName()
		}
		out = append(out, f)
	}
	for name, bt := range b.NestedBlocks {
		f := Field{Name: name, Kind: KindBlock, Type: "block", Nesting: string(bt.NestingMode), Required: bt.MinItems > 0, Optional: bt.MinItems == 0}
		if bt.Block != nil {
			f.Deprecated = bt.Block.Deprecated
		}
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Field) int { return cmp.Compare(a.Name, b.Name) })
	return out
}
