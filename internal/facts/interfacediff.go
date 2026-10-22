package facts

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// InterfaceDiff is a document of schema/interface-diff.schema.json: per area
// in scope, the interface elements that differ between the base and the
// head. Values are source text, read and never evaluated.
type InterfaceDiff struct {
	HeadSHA      string     `json:"head_sha"`
	MergeBaseSHA string     `json:"merge_base_sha"`
	Areas        []areaDiff `json:"areas"`
}

type areaDiff struct {
	Path            string                        `json:"path"`
	Kind            string                        `json:"kind"`
	RequiredVersion *versionChange                `json:"required_version"`
	Variables       []elemChange[variableValue]   `json:"variables"`
	Outputs         []elemChange[outputValue]     `json:"outputs"`
	Providers       []elemChange[providerValue]   `json:"providers"`
	Resources       []elemChange[resourceValue]   `json:"resources"`
	Moved           []elemChange[movedValue]      `json:"moved"`
	ModuleCalls     []elemChange[moduleCallValue] `json:"module_calls"`
}

// versionChange is the required_version constraint at base and at head.
type versionChange struct {
	Base *string `json:"base"`
	Head *string `json:"head"`
}

// elemChange is one element that differs. keyName is the JSON name of its
// key, which differs per section.
type elemChange[V any] struct {
	keyName, key string
	change       string
	base, head   *V
}

func (c elemChange[V]) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{c.keyName: c.key, "change": c.change, "base": c.base, "head": c.head})
}

type variableValue struct {
	Type        *string           `json:"type"`
	Default     *string           `json:"default"`
	Nullable    bool              `json:"nullable"`
	Sensitive   bool              `json:"sensitive"`
	Description *string           `json:"description"`
	Validation  []validationValue `json:"validation"`
}

type validationValue struct {
	Condition    string `json:"condition"`
	ErrorMessage string `json:"error_message"`
}

type outputValue struct {
	Value       *string `json:"value"`
	Sensitive   bool    `json:"sensitive"`
	Description *string `json:"description"`
}

type providerValue struct {
	Source               string   `json:"source"`
	VersionConstraint    *string  `json:"version_constraint"`
	ConfigurationAliases []string `json:"configuration_aliases"`
}

type resourceValue struct {
	Count   *string `json:"count"`
	ForEach *string `json:"for_each"`
	Prov    *string `json:"provider"`
}

type movedValue struct {
	To string `json:"to"`
}

type moduleCallValue struct {
	Source  string  `json:"source"`
	Version *string `json:"version"`
	Count   *string `json:"count"`
	ForEach *string `json:"for_each"`
}

// resourceAddr is the address pattern of the schema. A label outside it is
// one Terraform refuses, so such a block is left out.
var resourceAddr = regexp.MustCompile(`^(data\.)?[a-z][a-z0-9_]*\.[A-Za-z_][A-Za-z0-9_-]*$`)

// BuildInterfaceDiff compares the head and base modules of the areas in
// scope. It returns nil without both commits or without a base tree, since
// the document binds both and has nothing to compare against. An area is
// listed only when both revisions were read in full, so neither has a parse
// error, an unsupported file or an issue, and when at least one element
// differs. Of two elements with the same key in one module, which Terraform
// refuses, the first in file order counts. Version constraints written as
// plain strings are their value; every other expression is its source text.
// Expressions are compared by their tokens, so whitespace and comments do
// not count as a difference.
func BuildInterfaceDiff(headSHA, mergeBaseSHA *string, areas []*Area) *InterfaceDiff {
	if headSHA == nil || mergeBaseSHA == nil || slices.ContainsFunc(areas, func(a *Area) bool { return a.Base == nil }) {
		return nil
	}
	d := &InterfaceDiff{HeadSHA: *headSHA, MergeBaseSHA: *mergeBaseSHA, Areas: []areaDiff{}}
	for _, a := range areas {
		if a.Untouched || a.Issue != "" || len(a.Head.Unsupported) > 0 || len(a.Base.Unsupported) > 0 {
			continue
		}
		ad := areaDiff{
			Path:        a.Path,
			Kind:        a.Kind,
			Variables:   diffElems("name", ifVariables(a.Base), ifVariables(a.Head), variableValue.norm),
			Outputs:     diffElems("name", ifOutputs(a.Base), ifOutputs(a.Head), outputValue.norm),
			Providers:   diffElems("local_name", ifProviders(a.Base), ifProviders(a.Head), providerValue.norm),
			Resources:   diffElems("address", ifResources(a.Base), ifResources(a.Head), resourceValue.norm),
			Moved:       diffElems("from", ifMoved(a.Base), ifMoved(a.Head), movedValue.norm),
			ModuleCalls: diffElems("name", ifModuleCalls(a.Base), ifModuleCalls(a.Head), moduleCallValue.norm),
		}
		if b, h := ifRequiredVersion(a.Base), ifRequiredVersion(a.Head); !reflect.DeepEqual(b, h) {
			ad.RequiredVersion = &versionChange{Base: b, Head: h}
		}
		if ad.RequiredVersion != nil || len(ad.Variables)+len(ad.Outputs)+len(ad.Providers)+
			len(ad.Resources)+len(ad.Moved)+len(ad.ModuleCalls) > 0 {
			d.Areas = append(d.Areas, ad)
		}
	}
	return d
}

// diffElems pairs the base and head values by key and returns those that
// differ, sorted by key. norm puts a value in the form it is compared in.
func diffElems[V any](keyName string, base, head map[string]V, norm func(V) V) []elemChange[V] {
	keys := make([]string, 0, len(base)+len(head))
	for k := range base {
		keys = append(keys, k)
	}
	for k := range head {
		if _, ok := base[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := []elemChange[V]{}
	for _, k := range keys {
		b, inBase := base[k]
		h, inHead := head[k]
		c := elemChange[V]{keyName: keyName, key: k}
		switch {
		case !inBase:
			c.change, c.head = "added", &h
		case !inHead:
			c.change, c.base = "removed", &b
		case reflect.DeepEqual(norm(b), norm(h)):
			continue
		default:
			c.change, c.base, c.head = "changed", &b, &h
		}
		out = append(out, c)
	}
	return out
}

// ifPut adds v under k unless k is taken.
func ifPut[V any](m map[string]V, k string, v V) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

func ifVariables(m *Module) map[string]variableValue {
	out := map[string]variableValue{}
	for _, v := range m.Variables {
		val := variableValue{
			Default:     exprPtr(v.Default),
			Nullable:    v.Nullable == nil || strings.TrimSpace(v.Nullable.Text) != "false",
			Sensitive:   v.Sensitive != nil && strings.TrimSpace(v.Sensitive.Text) == "true",
			Description: exprPtr(v.Body.Arg("description")),
			Validation:  []validationValue{},
		}
		if v.HasType {
			val.Type = &v.TypeText
		}
		for _, vd := range v.Validations {
			val.Validation = append(val.Validation, validationValue{Condition: exprString(vd.Condition), ErrorMessage: exprString(vd.ErrorMessage)})
		}
		ifPut(out, v.Name, val)
	}
	return out
}

func ifOutputs(m *Module) map[string]outputValue {
	out := map[string]outputValue{}
	for _, o := range m.Outputs {
		ifPut(out, o.Name, outputValue{
			Value:       exprPtr(o.Value),
			Sensitive:   o.Sensitive != nil && strings.TrimSpace(o.Sensitive.Text) == "true",
			Description: exprPtr(o.Body.Arg("description")),
		})
	}
	return out
}

// ifProviders reads required_providers. A provider with no plain string
// source has the source Terraform implies, hashicorp/<local name>.
func ifProviders(m *Module) map[string]providerValue {
	out := map[string]providerValue{}
	for _, t := range m.Terraform {
		for _, rp := range t.RequiredProviders {
			v := providerValue{Source: rp.Source, ConfigurationAliases: append([]string{}, rp.Aliases...)}
			if v.Source == "" {
				v.Source = "hashicorp/" + rp.Name
			}
			if rp.Version != "" {
				v.VersionConstraint = &rp.Version
			}
			ifPut(out, rp.Name, v)
		}
	}
	return out
}

func ifResources(m *Module) map[string]resourceValue {
	out := map[string]resourceValue{}
	for _, r := range m.Resources {
		addr := r.Type + "." + r.Name
		if r.Mode == "data" {
			addr = "data." + addr
		}
		if !resourceAddr.MatchString(addr) {
			continue
		}
		ifPut(out, addr, resourceValue{Count: exprPtr(r.Body.Arg("count")), ForEach: exprPtr(r.Body.Arg("for_each")), Prov: exprPtr(r.Body.Arg("provider"))})
	}
	return out
}

// ifMoved is keyed by the canonical from address, or by the from address as
// written when it is not a static address. A block with no from or to is
// one Terraform refuses, so it is left out.
func ifMoved(m *Module) map[string]movedValue {
	out := map[string]movedValue{}
	for _, mv := range m.Moved {
		if mv.From != "" && mv.To != "" {
			ifPut(out, fromAddr(mv.FromAddr, mv.From), movedValue{To: mv.To})
		}
	}
	return out
}

// ifModuleCalls takes the source's value when it is a plain string and its
// text otherwise. A call with no source is left out.
func ifModuleCalls(m *Module) map[string]moduleCallValue {
	out := map[string]moduleCallValue{}
	for _, c := range m.ModuleCalls {
		src := c.Source
		if src == "" {
			src = exprString(c.Body.Arg("source"))
		}
		if src == "" {
			continue
		}
		ifPut(out, c.Name, moduleCallValue{Source: src, Version: ifConstraint(c.Body.Arg("version")),
			Count: exprPtr(c.Body.Arg("count")), ForEach: exprPtr(c.Body.Arg("for_each"))})
	}
	return out
}

// ifRequiredVersion joins every required_version of the module's terraform
// blocks in file order with ", ", since Terraform enforces each one. It is
// nil when there is none.
func ifRequiredVersion(m *Module) *string {
	var parts []string
	for _, t := range m.Terraform {
		if c := ifConstraint(t.RequiredVersion); c != nil {
			parts = append(parts, *c)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	s := strings.Join(parts, ", ")
	return &s
}

// ifConstraint returns a version constraint: the value of a plain quoted
// string, or the source text of anything else.
func ifConstraint(e *Expr) *string {
	if e == nil {
		return nil
	}
	s := e.Text
	if strings.HasPrefix(s, `"`) && !strings.Contains(s, "${") && !strings.Contains(s, "%{") {
		if u, err := strconv.Unquote(s); err == nil {
			s = u
		}
	}
	return &s
}

func exprPtr(e *Expr) *string {
	if e == nil {
		return nil
	}
	return &e.Text
}

func exprString(e *Expr) string {
	if e == nil {
		return ""
	}
	return e.Text
}

// normExpr returns the tokens of an expression joined by one space, with
// whitespace, newlines and comments left out, or the text itself when it
// does not lex.
func normExpr(s string) string {
	tokens, diags := hclsyntax.LexExpression([]byte(s), "", hcl.InitialPos)
	if diags.HasErrors() {
		return s
	}
	parts := make([]string, 0, len(tokens))
	for _, tk := range tokens {
		switch tk.Type {
		case hclsyntax.TokenComment, hclsyntax.TokenNewline, hclsyntax.TokenEOF:
		default:
			parts = append(parts, string(tk.Bytes))
		}
	}
	return strings.Join(parts, " ")
}

func normPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := normExpr(*p)
	return &s
}

// norm returns the value with every expression in token form, to compare.
func (v variableValue) norm() variableValue {
	v.Type, v.Default, v.Description = normPtr(v.Type), normPtr(v.Default), normPtr(v.Description)
	vs := make([]validationValue, len(v.Validation))
	for i, vd := range v.Validation {
		vs[i] = validationValue{Condition: normExpr(vd.Condition), ErrorMessage: normExpr(vd.ErrorMessage)}
	}
	v.Validation = vs
	return v
}

func (v outputValue) norm() outputValue {
	v.Value, v.Description = normPtr(v.Value), normPtr(v.Description)
	return v
}

func (v providerValue) norm() providerValue {
	as := make([]string, len(v.ConfigurationAliases))
	for i, a := range v.ConfigurationAliases {
		as[i] = normExpr(a)
	}
	v.ConfigurationAliases = as
	return v
}

func (v resourceValue) norm() resourceValue {
	v.Count, v.ForEach, v.Prov = normPtr(v.Count), normPtr(v.ForEach), normPtr(v.Prov)
	return v
}

func (v movedValue) norm() movedValue { return movedValue{To: normExpr(v.To)} }

func (v moduleCallValue) norm() moduleCallValue {
	v.Count, v.ForEach = normPtr(v.Count), normPtr(v.ForEach)
	return v
}
