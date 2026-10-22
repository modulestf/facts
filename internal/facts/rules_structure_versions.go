package facts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/modulestf/facts/data"
)

// structureVersionsRules is the structure_versions rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// Each is a candidate rule. A check emits a candidate for every construct its
// detector matches, touched or not, and the engine keeps those its scope
// judges; the cross-area rules keep only those on a changed line. What a
// detector cannot decide, such as a feature the core feature table does not
// list or a constraint that is not a plain string, is a candidate too, never
// clean: no reason code on the closed list says "undecidable".
var structureVersionsRules = []Rule{
	{ID: "structure.feature-above-version-floor", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkFeatureAboveFloor},
	{ID: "structure.passthrough-default-drift", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkPassthroughDefaultDrift},
	{ID: "structure.passthrough-field-drift", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkPassthroughFieldDrift},
	{ID: "structure.version-floor-understated", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkVersionFloorUnderstated},
}

// coreFeature is one row of data/core-features.json.
type coreFeature struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Match      string `json:"match"`
	Introduced string `json:"introduced"`
	Source     string `json:"source"`
	version    *version.Version
}

// coreFeatures is the pinned table by id. A bad table is a programming error,
// caught when the package loads.
var coreFeatures = loadCoreFeatures(data.CoreFeatures)

var featureKinds = map[string]bool{"block": true, "meta_argument": true, "function": true, "type_constraint": true, "argument": true, "expression": true}

func loadCoreFeatures(b []byte) map[string]*coreFeature {
	var rows []*coreFeature
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&rows); err != nil {
		panic("facts: core-features.json: " + err.Error())
	}
	out := map[string]*coreFeature{}
	for _, r := range rows {
		v, err := version.NewVersion(r.Introduced)
		switch {
		case err != nil || v.String() != r.Introduced || len(strings.Split(r.Introduced, ".")) != 3:
			panic("facts: core-features.json: bad introduced version of " + r.ID)
		case !featureKinds[r.Kind] || r.ID == "" || out[r.ID] != nil || !strings.HasPrefix(r.Source, "https://"):
			panic("facts: core-features.json: bad row " + r.ID)
		}
		r.version = v
		out[r.ID] = r
	}
	return out
}

// feature returns a row of the table. An id the table lacks is a programming
// error.
func feature(id string) *coreFeature {
	r := coreFeatures[id]
	if r == nil {
		panic("facts: no core feature " + id)
	}
	return r
}

// tableBaseline is the release the table's coverage starts at: every feature
// Terraform v1.16 detects in HCL that is not a row, such as the functions in
// terraformFunctions with no row, was in Terraform 0.13.0. Below it the table
// cannot tell, so every touched block is a candidate.
var tableBaseline = version.Must(version.NewVersion("0.13.0"))

// terraformFunctions are the built-in functions of Terraform v1.16.7, from
// baseFunctions and Scope.Functions in
// https://github.com/hashicorp/terraform/blob/v1.16.7/internal/lang/functions.go
// (read 2026-10-22). type is left out: only the console has it. A call to any
// other name, or in the core:: namespace, is a feature the table does not
// list.
var terraformFunctions = toSet("abs", "abspath", "alltrue", "anytrue", "basename", "base64decode", "base64encode",
	"base64gzip", "base64sha256", "base64sha512", "bcrypt", "can", "ceil", "chomp", "cidrhost", "cidrnetmask",
	"cidrsubnet", "cidrsubnets", "coalesce", "coalescelist", "compact", "concat", "contains", "convert", "csvdecode",
	"dirname", "distinct", "element", "endswith", "ephemeralasnull", "chunklist", "file", "fileexists", "fileset",
	"filebase64", "filebase64sha256", "filebase64sha512", "filemd5", "filesha1", "filesha256", "filesha512",
	"flatten", "floor", "format", "formatdate", "formatlist", "indent", "index", "join", "jsondecode", "jsonencode",
	"keys", "length", "list", "log", "lookup", "lower", "map", "matchkeys", "max", "md5", "merge", "min", "one",
	"parseint", "pathexpand", "plantimestamp", "pow", "range", "regex", "regexall", "replace", "reverse",
	"rsadecrypt", "sensitive", "nonsensitive", "issensitive", "setintersection", "setproduct", "setsubtract",
	"setunion", "sha1", "sha256", "sha512", "signum", "slice", "sort", "split", "startswith", "strcontains",
	"strrev", "substr", "sum", "templatefile", "templatestring", "textdecodebase64", "textencodebase64",
	"timestamp", "timeadd", "timecmp", "title", "tostring", "tonumber", "tobool", "toset", "tolist", "tomap",
	"transpose", "trim", "trimprefix", "trimspace", "trimsuffix", "try", "upper", "urlencode", "uuid", "uuidv5",
	"values", "yamldecode", "yamlencode", "zipmap")

// functionRows maps a function name to the table row of the release that
// introduced it.
var functionRows = map[string]string{
	"sum": "function_sum", "alltrue": "function_alltrue", "anytrue": "function_anytrue",
	"textencodebase64": "function_textencodebase64", "textdecodebase64": "function_textdecodebase64",
	"one": "function_one", "sensitive": "function_sensitive", "nonsensitive": "function_nonsensitive",
	"startswith": "function_startswith", "endswith": "function_endswith", "timecmp": "function_timecmp",
	"plantimestamp": "function_plantimestamp", "strcontains": "function_strcontains",
	"issensitive": "function_issensitive", "templatestring": "function_templatestring",
	"ephemeralasnull": "function_ephemeralasnull", "convert": "function_convert",
	"provider::terraform::encode_tfvars": "function_terraform_encode_tfvars",
	"provider::terraform::decode_tfvars": "function_terraform_decode_tfvars",
	"provider::terraform::encode_expr":   "function_terraform_encode_expr",
}

func toSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Names Terraform knows in each place the walk checks. Anything else is a
// feature the table does not list.
var (
	knownVariableArgs  = toSet("type", "default", "description", "sensitive", "nullable", "ephemeral", "deprecated")
	knownOutputArgs    = toSet("value", "description", "sensitive", "depends_on", "ephemeral", "deprecated", "type")
	knownLifecycleArgs = toSet("create_before_destroy", "prevent_destroy", "ignore_changes", "replace_triggered_by", "destroy")
	knownTerraformArgs = toSet("required_version", "experiments")
	knownTerraformSubs = toSet("required_providers", "backend", "provider_meta")
	typePrimitives     = toSet("string", "number", "bool", "any")
)

// featureUse is one construct the walk found: a row of the table, or nil row
// for a feature the table does not list. file is relative to the area. A row
// that refines a construct, such as import_for_each, sits on the construct's
// line, so the construct's highest row decides.
type featureUse struct {
	file string
	line int
	row  *coreFeature
	text string
}

type featureWalker struct {
	uses []featureUse
}

func (w *featureWalker) add(file string, line int, id, text string) {
	var row *coreFeature
	if id != "" {
		row = feature(id)
	}
	w.uses = append(w.uses, featureUse{file: file, line: line, row: row, text: text})
}

// expr records the function calls of an expression.
func (w *featureWalker) expr(file string, x Expr) {
	e := reparse(file, x)
	if e == nil {
		w.add(file, x.Range.Start.Line, "", x.Text)
		return
	}
	w.node(file, x, e)
}

// node records the function calls under e. The type argument of convert() is
// read as a type constraint.
func (w *featureWalker) node(file string, x Expr, e hclsyntax.Expression) {
	var typeArgs []hcl.Range
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		c, ok := n.(*hclsyntax.FunctionCallExpr)
		if !ok || slices.ContainsFunc(typeArgs, func(r hcl.Range) bool { return within(c.Range(), r) }) {
			return nil
		}
		line, text := c.NameRange.Start.Line, sourceOf(x, c.Range())
		switch id, known := functionRows[c.Name]; {
		case known:
			w.add(file, line, id, text)
		case strings.HasPrefix(c.Name, "provider::"):
			w.add(file, line, "provider_defined_function", text)
		case !terraformFunctions[c.Name]:
			w.add(file, line, "", text)
		}
		if c.Name == "convert" && len(c.Args) == 2 {
			typeArgs = append(typeArgs, c.Args[1].Range())
			w.typeNode(file, x, c.Args[1])
		}
		return nil
	})
}

func within(r, outer hcl.Range) bool {
	return r.Start.Byte >= outer.Start.Byte && r.End.Byte <= outer.End.Byte
}

// typeExpr records the optional() attributes of a type constraint, and any
// keyword a type constraint does not have.
func (w *featureWalker) typeExpr(file string, x Expr) {
	e := reparse(file, x)
	if e == nil {
		w.add(file, x.Range.Start.Line, "", x.Text)
		return
	}
	w.typeNode(file, x, e)
}

func (w *featureWalker) typeNode(file string, x Expr, e hclsyntax.Expression) {
	unknown := func() { w.add(file, e.Range().Start.Line, "", sourceOf(x, e.Range())) }
	switch t := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(t.Traversal) != 1 || !typePrimitives[t.Traversal.RootName()] {
			unknown()
		}
	case *hclsyntax.FunctionCallExpr:
		switch {
		case (t.Name == "list" || t.Name == "set" || t.Name == "map") && len(t.Args) == 1:
			w.typeNode(file, x, t.Args[0])
		case t.Name == "object" && len(t.Args) == 1:
			obj, ok := t.Args[0].(*hclsyntax.ObjectConsExpr)
			if !ok {
				unknown()
				return
			}
			for _, it := range obj.Items {
				w.typeNode(file, x, it.ValueExpr)
			}
		case t.Name == "tuple" && len(t.Args) == 1:
			tup, ok := t.Args[0].(*hclsyntax.TupleConsExpr)
			if !ok {
				unknown()
				return
			}
			for _, el := range tup.Exprs {
				w.typeNode(file, x, el)
			}
		case t.Name == "optional" && len(t.Args) == 1:
			w.add(file, t.NameRange.Start.Line, "type_optional_attribute", sourceOf(x, t.Range()))
			w.typeNode(file, x, t.Args[0])
		case t.Name == "optional" && len(t.Args) == 2:
			w.add(file, t.NameRange.Start.Line, "type_optional_attribute_default", sourceOf(x, t.Range()))
			w.typeNode(file, x, t.Args[0])
			w.node(file, x, t.Args[1])
		default:
			unknown()
		}
	default:
		unknown()
	}
}

// body records the function calls of every argument of a body, nested blocks
// included.
func (w *featureWalker) body(file string, b Body) {
	walkBody(b, "", false, func(_ string, x Expr) { w.expr(file, x) })
}

// featureUses walks every top-level block of a module.
func featureUses(m *Module) []featureUse {
	w := &featureWalker{}
	for _, v := range m.Variables {
		w.variable(v)
	}
	for _, o := range m.Outputs {
		w.output(o)
	}
	for _, l := range m.Locals {
		w.expr(l.File, l.Value)
	}
	for _, t := range m.Terraform {
		w.terraform(t)
	}
	for _, p := range m.Providers {
		w.body(p.File, p.Body)
	}
	for _, r := range m.Resources {
		w.resource(r)
	}
	for _, c := range m.ModuleCalls {
		w.module(c)
	}
	for _, mv := range m.Moved {
		w.moved(mv)
	}
	for _, rm := range m.Removed {
		w.add(rm.File, rm.Line, "removed_block", "removed")
		for _, nb := range rm.Body.Blocks {
			if nb.Type == "provisioner" {
				w.add(rm.File, rm.Line, "removed_block_provisioner", "provisioner")
			}
		}
		w.body(rm.File, rm.Body)
	}
	for _, o := range m.Other {
		w.other(o)
	}
	return w.uses
}

func (w *featureWalker) variable(v Variable) {
	rows := map[string]string{"sensitive": "variable_sensitive", "nullable": "variable_nullable", "ephemeral": "variable_ephemeral", "deprecated": "variable_deprecated"}
	for _, at := range v.Body.Attributes {
		line := at.Range.Start.Line
		switch {
		case at.Name == "type":
			w.typeExpr(v.File, at.Value)
			continue
		case rows[at.Name] != "":
			w.add(v.File, line, rows[at.Name], at.Value.Text)
		case !knownVariableArgs[at.Name]:
			w.add(v.File, line, "", at.Name)
		}
		w.expr(v.File, at.Value)
	}
	for _, nb := range v.Body.Blocks {
		if nb.Type != "validation" || nb.Dynamic {
			w.add(v.File, nb.Range.Start.Line, "", nb.Type)
			continue
		}
		w.add(v.File, nb.Range.Start.Line, "variable_validation", "validation")
		for _, at := range nb.Body.Attributes {
			if at.Name != "condition" && at.Name != "error_message" {
				w.add(v.File, at.Range.Start.Line, "", at.Name)
			} else if crossObject(v.File, v.Name, at.Value) {
				w.add(v.File, at.Range.Start.Line, "variable_validation_cross_object", at.Value.Text)
			}
			w.expr(v.File, at.Value)
		}
		for _, inner := range nb.Body.Blocks {
			w.add(v.File, inner.Range.Start.Line, "", inner.Type)
		}
	}
}

// crossObject reports whether a validation expression reads anything other
// than var.<name>. One that does not parse again counts as reading more.
func crossObject(file, name string, x Expr) bool {
	e := reparse(file, x)
	if e == nil {
		return true
	}
	for _, t := range e.Variables() {
		if t.RootName() != "var" || len(t) < 2 {
			return true
		}
		if a, ok := t[1].(hcl.TraverseAttr); !ok || a.Name != name {
			return true
		}
	}
	return false
}

func (w *featureWalker) output(o Output) {
	rows := map[string]string{"sensitive": "output_sensitive", "ephemeral": "output_ephemeral", "deprecated": "output_deprecated"}
	for _, at := range o.Body.Attributes {
		line := at.Range.Start.Line
		switch {
		case at.Name == "type":
			w.add(o.File, line, "output_type", at.Value.Text)
			w.typeExpr(o.File, at.Value)
			continue
		case rows[at.Name] != "":
			w.add(o.File, line, rows[at.Name], at.Value.Text)
		case !knownOutputArgs[at.Name]:
			w.add(o.File, line, "", at.Name)
		}
		w.expr(o.File, at.Value)
	}
	for _, nb := range o.Body.Blocks {
		if nb.Type == "precondition" && !nb.Dynamic {
			w.add(o.File, nb.Range.Start.Line, "precondition_block", "precondition")
		} else {
			w.add(o.File, nb.Range.Start.Line, "", nb.Type)
		}
		w.body(o.File, nb.Body)
	}
}

func (w *featureWalker) terraform(t Terraform) {
	for _, at := range t.Body.Attributes {
		if !knownTerraformArgs[at.Name] {
			w.add(t.File, at.Range.Start.Line, "", at.Name)
		}
	}
	for _, nb := range t.Body.Blocks {
		if !knownTerraformSubs[nb.Type] || nb.Dynamic {
			w.add(t.File, nb.Range.Start.Line, "", nb.Type)
			continue
		}
		if nb.Type != "required_providers" {
			continue
		}
		for _, at := range nb.Body.Attributes {
			e := reparse(t.File, at.Value)
			obj, ok := e.(*hclsyntax.ObjectConsExpr)
			if !ok {
				continue // the legacy string form is a version constraint
			}
			for _, it := range obj.Items {
				switch k := keyName(it.KeyExpr); k {
				case "source", "version":
				case "configuration_aliases":
					w.add(t.File, it.KeyExpr.Range().Start.Line, "required_providers_configuration_aliases", sourceOf(at.Value, it.ValueExpr.Range()))
				default:
					w.add(t.File, it.KeyExpr.Range().Start.Line, "", k)
				}
			}
		}
	}
}

func (w *featureWalker) resource(r Resource) {
	if r.Mode == "resource" && r.Type == "terraform_data" {
		w.add(r.File, r.Line, "resource_terraform_data", "terraform_data")
	}
	for _, nb := range r.Body.Blocks {
		switch {
		case nb.Dynamic:
		case nb.Type == "store" && r.Mode == "resource" && r.Type == "terraform_data":
			w.add(r.File, r.Line, "terraform_data_store", "store")
		case nb.Type == "lifecycle":
			w.lifecycle(r.File, nb.Body)
		}
	}
	w.body(r.File, r.Body)
}

func (w *featureWalker) lifecycle(file string, b Body) {
	rows := map[string]string{"replace_triggered_by": "lifecycle_replace_triggered_by", "destroy": "lifecycle_destroy_false"}
	for _, at := range b.Attributes {
		if rows[at.Name] != "" {
			w.add(file, at.Range.Start.Line, rows[at.Name], at.Value.Text)
		} else if !knownLifecycleArgs[at.Name] {
			w.add(file, at.Range.Start.Line, "", at.Name)
		}
	}
	for _, nb := range b.Blocks {
		line := nb.Range.Start.Line
		switch {
		case nb.Dynamic:
			w.add(file, line, "", "dynamic "+nb.Type)
		case nb.Type == "precondition":
			w.add(file, line, "precondition_block", nb.Type)
		case nb.Type == "postcondition":
			w.add(file, line, "postcondition_block", nb.Type)
		case nb.Type == "action_trigger":
			w.add(file, line, "lifecycle_action_trigger", nb.Type)
			if at := nb.Body.Arg("on_failure"); at != nil {
				w.add(file, line, "action_trigger_on_failure", at.Text)
			}
			if at := nb.Body.Arg("events"); at != nil && destroyEvent(file, *at) {
				w.add(file, line, "action_trigger_destroy_events", at.Text)
			}
		default:
			w.add(file, line, "", nb.Type)
		}
	}
}

// destroyEvent reports whether an events list names a destroy event. A list
// that does not parse again counts as naming one.
func destroyEvent(file string, x Expr) bool {
	e := reparse(file, x)
	if e == nil {
		return true
	}
	found := false
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if st, ok := n.(*hclsyntax.ScopeTraversalExpr); ok && len(st.Traversal) == 1 {
			found = found || st.Traversal.RootName() == "before_destroy" || st.Traversal.RootName() == "after_destroy"
		}
		return nil
	})
	return found
}

func (w *featureWalker) module(c ModuleCall) {
	rows := map[string]string{"count": "module_count", "for_each": "module_for_each", "depends_on": "module_depends_on"}
	for _, at := range c.Body.Attributes {
		line := at.Range.Start.Line
		if rows[at.Name] != "" {
			w.add(c.File, line, rows[at.Name], at.Value.Text)
		}
		if (at.Name == "source" || at.Name == "version") && readsInput(c.File, at.Value) {
			w.add(c.File, line, "module_source_version_variables", at.Value.Text)
		}
	}
	for _, nb := range c.Body.Blocks {
		w.add(c.File, nb.Range.Start.Line, "", nb.Type)
	}
	w.body(c.File, c.Body)
}

// readsInput reports whether an expression references var or local. One that
// does not parse again counts as reading one.
func readsInput(file string, x Expr) bool {
	e := reparse(file, x)
	if e == nil {
		return true
	}
	return slices.ContainsFunc(e.Variables(), func(t hcl.Traversal) bool { return t.RootName() == "var" || t.RootName() == "local" })
}

func (w *featureWalker) moved(mv Moved) {
	w.add(mv.File, mv.Line, "moved_block", "moved")
	from, fromOK := movedType(mv.FromAddr)
	to, toOK := movedType(mv.ToAddr)
	switch {
	case mv.FromAddr == "" || mv.ToAddr == "":
		w.add(mv.File, mv.Line, "", "moved from "+mv.From+" to "+mv.To)
	case fromOK && toOK && from != to:
		w.add(mv.File, mv.Line, "moved_cross_resource_type", "moved from "+mv.From+" to "+mv.To)
		if from == "null_resource" && to == "terraform_data" {
			w.add(mv.File, mv.Line, "moved_null_resource_to_terraform_data", "moved from "+mv.From+" to "+mv.To)
		}
	}
}

// movedType returns the resource type of a canonical moved address, after
// any module.<name> steps. ok is false for a module address.
func movedType(addr string) (typ string, ok bool) {
	var names []string
	var cur strings.Builder
	depth, quoted := 0, false
	for i := 0; i < len(addr); i++ {
		switch c := addr[i]; {
		case quoted && c == '\\':
			i++
		case quoted:
			quoted = c != '"'
		case c == '"':
			quoted = true
		case c == '[':
			depth++
		case c == ']':
			depth--
		case depth > 0:
		case c == '.':
			names = append(names, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	names = append(names, cur.String())
	for len(names) >= 2 && names[0] == "module" {
		names = names[2:]
	}
	if len(names) == 3 && names[0] == "data" {
		return "data." + names[1], true
	}
	if len(names) != 2 {
		return "", false
	}
	return names[0], true
}

func (w *featureWalker) other(o OtherBlock) {
	switch o.Type {
	case "locals":
		return // its values are walked as Locals
	case "check":
		w.add(o.File, o.Line, "check_block", "check")
	case "ephemeral":
		w.add(o.File, o.Line, "ephemeral_resource_block", "ephemeral")
	case "action":
		w.add(o.File, o.Line, "action_block", "action")
	case "import":
		// Every area this rule reads is a child module, so an import block
		// here matches import_block_in_child_module as well.
		w.add(o.File, o.Line, "import_block", "import")
		w.add(o.File, o.Line, "import_block_in_child_module", "import")
		if at := o.Body.Arg("for_each"); at != nil {
			w.add(o.File, o.Line, "import_for_each", at.Text)
		}
		if at := o.Body.Arg("identity"); at != nil {
			w.add(o.File, o.Line, "import_identity", at.Text)
		}
	default:
		w.add(o.File, o.Line, "", o.Type)
		return
	}
	w.body(o.File, o.Body)
}

// versionDecl is the required_version constraints of one module directory.
type versionDecl struct {
	cs version.Constraints
	// lines are the required_version arguments, file relative to the area.
	lines []declLine
	// ok is false when a constraint is not a plain string or does not parse.
	ok bool
}

type declLine struct {
	file string
	line int
}

func requiredVersions(m *Module) versionDecl {
	d := versionDecl{ok: true}
	for _, t := range m.Terraform {
		rv := t.RequiredVersion
		if rv == nil {
			continue
		}
		d.lines = append(d.lines, declLine{t.File, rv.Range.Start.Line})
		s, err := strconv.Unquote(rv.Text)
		if err != nil || strings.Contains(rv.Text, "${") || strings.Contains(rv.Text, "%{") {
			d.ok = false
			continue
		}
		cs, err := version.NewConstraint(s)
		if err != nil {
			d.ok = false
			continue
		}
		d.cs = append(d.cs, cs...)
	}
	return d
}

// lowestAdmitted returns the lowest release that satisfies every constraint,
// or nil when none does. The lowest point of such a set is 0.0.0, a bound's
// own version or the patch after it, so only those are tried.
func lowestAdmitted(cs version.Constraints) *version.Version {
	try := []*version.Version{version.Must(version.NewVersion("0.0.0"))}
	for _, c := range cs {
		v, err := version.NewVersion(strings.TrimLeft(c.String(), "=!<>~ "))
		if err != nil {
			return nil
		}
		s := v.Segments64()
		next := version.Must(version.NewVersion(fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]+1)))
		try = append(try, version.Must(version.NewVersion(fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]))), next)
	}
	slices.SortFunc(try, func(a, b *version.Version) int { return a.Compare(b) })
	for _, v := range try {
		if cs.Check(v) {
			return v
		}
	}
	return nil
}

// structure.feature-above-version-floor: a construct of the core feature
// table introduced after the lowest release the area's required_version
// admits. A construct with more than one row takes the row with the highest
// version. A feature the table does not list is a candidate. With no
// required_version there is no declared floor to compare with, so every row
// of the table and every unlisted feature is a candidate. With a constraint
// that is not a plain string, admits no release, or has a floor below the
// table's baseline, every block is a candidate: the table cannot decide
// there. Its evidence is the construct's line and the required_version lines.
func checkFeatureAboveFloor(a *Area) Result {
	const id = "structure.feature-above-version-floor"
	decl := requiredVersions(a.Head)
	floor := version.Must(version.NewVersion("0.0.0"))
	if len(decl.lines) > 0 {
		floor = nil
		if decl.ok {
			floor = lowestAdmitted(decl.cs)
		}
	}
	newCand := func(file string, line int, addr, head string) Candidate {
		e := newEvidence(a)
		e.line(file, line)
		for _, d := range decl.lines {
			e.line(d.file, d.line)
		}
		e.head(head)
		return e.candidate(id, file, line, addr)
	}
	var res Result
	if floor == nil || len(decl.lines) > 0 && floor.LessThan(tableBaseline) {
		for _, b := range topBlocks(a.Head) {
			if b.addr != "locals" {
				text, _ := headLine(a, b.File, b.Line)
				res.Candidates = append(res.Candidates, newCand(b.File, b.Line, b.addr, strings.TrimSpace(text)))
			}
		}
		return res
	}
	idx := moduleIndex(nil, a.Head)
	type key struct {
		file string
		line int
		addr string
	}
	worst := map[key]featureUse{}
	var order []key
	for _, u := range featureUses(a.Head) {
		addr := SubjectAt(idx, u.file, u.line)
		if addr == "" {
			addr = u.file
		}
		k := key{u.file, u.line, addr}
		prev, seen := worst[k]
		if !seen {
			order = append(order, k)
		}
		if !seen || (prev.row != nil && (u.row == nil || u.row.version.GreaterThan(prev.row.version))) {
			worst[k] = u
		}
	}
	for _, k := range order {
		u := worst[k]
		if u.row == nil || u.row.version.GreaterThan(floor) {
			res.Candidates = append(res.Candidates, newCand(k.file, k.line, k.addr, u.text))
		}
	}
	return res
}

// lineChanges answers whether a head line is changed, from the trees of an
// area, with one diff per file.
type lineChanges struct {
	a     *Area
	cache map[string][]lineRange
}

func (lc *lineChanges) changed(file string, line int) bool {
	r, ok := lc.cache[file]
	if !ok {
		if h := lc.a.headTree.Entries[file]; h != nil && h.Kind == KindFile && !unchanged(lc.a, file) {
			var base []byte
			if lc.a.baseTree != nil {
				if b := lc.a.baseTree.Entries[file]; b != nil && b.Kind == KindFile {
					base = b.Data
				}
			}
			r = headChanged(base, h.Data)
		}
		lc.cache[file] = r
	}
	return inRanges(r, line)
}

// anchor is a line a cross-area candidate may land on, file relative to the
// module root.
type anchor struct {
	file string
	line int
}

// crossCandidate builds a cross-area candidate. It lands on the first anchor
// that is a changed line, the change that causes it, or on the first anchor
// when none is, which the engine then drops. Every anchor is a source line.
func crossCandidate(a *Area, lc *lineChanges, rule, addr string, anchors []anchor, base, head *string) Candidate {
	e := newEvidence(a)
	at := anchors[0]
	found := false
	for _, an := range anchors {
		if !found && lc.changed(an.file, an.line) {
			at, found = an, true
		}
	}
	seen := map[anchor]bool{}
	for _, an := range anchors {
		if seen[an] {
			continue
		}
		seen[an] = true
		if len(e.ev.SourceLines) == maxEvidenceComments {
			e.complete = false
			break
		}
		text, _ := treeLine(a.headTree, an.file, an.line)
		e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: "head", File: an.file, Line: an.line, Text: e.trim(text)})
	}
	if base != nil {
		e.base(*base)
	}
	if head != nil {
		e.head(*head)
	}
	line := at.line
	return Candidate{RuleID: rule, File: at.file, Line: &line, Subject: subject(a.Path, e.trim(addr)), Evidence: e.ev, EvidenceComplete: e.complete}
}

// structure.version-floor-understated: an area whose required_version admits
// a lower release than a local module it calls, or no release that module
// admits. A constraint that is not a plain string is a candidate. One
// candidate per caller, subject <area>:required_version, lands on the
// caller's required_version, else the callee's, else the call's source,
// whichever the change touches first.
func checkVersionFloorUnderstated(a *Area) Result {
	const id = "structure.version-floor-understated"
	caller := requiredVersions(a.Head)
	var anchors []anchor
	for _, d := range caller.lines {
		anchors = append(anchors, anchor{areaFile(a.Path, d.file), d.line})
	}
	var calls []ModuleCall
	offending := map[string]bool{}
	for _, call := range a.Head.ModuleCalls {
		callee := calledArea(a, call)
		if callee == nil {
			continue
		}
		if _, done := offending[callee.Path]; !done {
			d := requiredVersions(callee.Head)
			offending[callee.Path] = understated(caller, d)
			for _, l := range d.lines {
				if offending[callee.Path] {
					anchors = append(anchors, anchor{areaFile(callee.Path, l.file), l.line})
				}
			}
		}
		if offending[callee.Path] {
			calls = append(calls, call)
		}
	}
	for _, call := range calls {
		anchors = append(anchors, anchor{areaFile(a.Path, call.File), call.SourceRange.Start.Line})
	}
	if len(calls) == 0 {
		return Result{}
	}
	lc := &lineChanges{a: a, cache: map[string][]lineRange{}}
	var base *string
	if a.Base != nil {
		base = ifRequiredVersion(a.Base)
	}
	return Result{Candidates: []Candidate{crossCandidate(a, lc, id, "required_version", anchors, base, ifRequiredVersion(a.Head))}}
}

// understated reports whether a caller's constraints admit a lower release
// than its callee's, or no release the callee's admit too. Either side not
// readable counts. A caller that declares no required_version declares no
// floor, which examples.versions-missing reports for an example, and a callee
// with none admits every release, so neither is understated.
func understated(caller, callee versionDecl) bool {
	switch {
	case len(caller.lines) == 0 || len(callee.lines) == 0:
		return false
	case !caller.ok || !callee.ok:
		return true
	}
	low, calleeLow := lowestAdmitted(caller.cs), lowestAdmitted(callee.cs)
	if low == nil || calleeLow == nil || lowestAdmitted(append(slices.Clone(caller.cs), callee.cs...)) == nil {
		return true
	}
	return low.LessThan(calleeLow)
}

// structure.passthrough-default-drift and structure.passthrough-field-drift
// follow the reviewer's pass-through procedure: every call of a local module
// from a root or submodule area, the arguments it forwards, and the two types
// and unset values compared. An argument that is not forwarding, such as a
// literal, a local or merge(), is not compared, so it is clean. An unset
// value written differently from the submodule's is a candidate, since the
// engine compares source text and never evaluates. A forwarded attribute
// path whose unset value depends on a non-empty variable default is a
// candidate too.
func checkPassthroughDefaultDrift(a *Area) Result {
	return passthrough(a, "structure.passthrough-default-drift")
}

func checkPassthroughFieldDrift(a *Area) Result {
	return passthrough(a, "structure.passthrough-field-drift")
}

// Kinds of a passType.
const (
	passAny = iota
	passObject
	passColl
	passOther
)

// passType is a type constraint as the procedure walks it: collections are
// stepped through, object attributes are compared.
type passType struct {
	kind  int
	elem  *passType
	attrs []*passAttr
	line  int // of the object(...) expression
}

type passAttr struct {
	name     string
	optional bool
	def      *string // the optional() default; nil for optional(type) or a required attribute
	line     int
	typ      *passType
}

func (t *passType) attr(name string) *passAttr {
	for _, at := range t.attrs {
		if at.name == name {
			return at
		}
	}
	return nil
}

// unset is the attribute's unset value, or ok false for a required one.
func (at *passAttr) unset() (string, bool) {
	switch {
	case !at.optional:
		return "", false
	case at.def != nil:
		return *at.def, true
	}
	return "null", true
}

// varType reads a variable's type. No type is any.
func varType(v *Variable) *passType {
	x := v.Body.Arg("type")
	if x == nil {
		return &passType{kind: passAny}
	}
	e := reparse(v.File, *x)
	if e == nil {
		return &passType{kind: passOther}
	}
	return passTypeOf(*x, e)
}

func passTypeOf(x Expr, e hclsyntax.Expression) *passType {
	switch t := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(t.Traversal) == 1 && t.Traversal.RootName() == "any" {
			return &passType{kind: passAny}
		}
	case *hclsyntax.FunctionCallExpr:
		switch {
		case (t.Name == "list" || t.Name == "set" || t.Name == "map") && len(t.Args) == 1:
			return &passType{kind: passColl, elem: passTypeOf(x, t.Args[0])}
		case t.Name == "object" && len(t.Args) == 1:
			obj, ok := t.Args[0].(*hclsyntax.ObjectConsExpr)
			if !ok {
				break
			}
			out := &passType{kind: passObject, line: e.Range().Start.Line}
			for _, it := range obj.Items {
				at := &passAttr{name: keyName(it.KeyExpr), line: it.KeyExpr.Range().Start.Line}
				inner := it.ValueExpr
				if opt, ok := inner.(*hclsyntax.FunctionCallExpr); ok && opt.Name == "optional" && len(opt.Args) >= 1 && len(opt.Args) <= 2 {
					at.optional, inner = true, opt.Args[0]
					if len(opt.Args) == 2 {
						d := sourceOf(x, opt.Args[1].Range())
						at.def = &d
					}
				}
				at.typ = passTypeOf(x, inner)
				out.attrs = append(out.attrs, at)
			}
			return out
		}
	}
	return &passType{kind: passOther}
}

// passRef is one forwarded reference: var.<name> or each.value, then an
// attribute path.
type passRef struct {
	each bool
	name string
	path []string
}

// forwardRefs returns the references an argument forwards: one reference, or
// several joined only by coalesce(), try() or <ref> != null ? <ref> : <other>.
// ok is false for any other expression. each.value counts only when eachOK.
func forwardRefs(e hclsyntax.Expression, eachOK bool) (refs []passRef, ok bool) {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return forwardRefs(x.Expression, eachOK)
	case *hclsyntax.ScopeTraversalExpr:
		r, ok := traversalRef(x.Traversal, eachOK)
		return []passRef{r}, ok
	case *hclsyntax.FunctionCallExpr:
		if (x.Name != "coalesce" && x.Name != "try") || len(x.Args) == 0 || x.ExpandFinal {
			return nil, false
		}
		for _, arg := range x.Args {
			rs, ok := forwardRefs(arg, eachOK)
			if !ok {
				return nil, false
			}
			refs = append(refs, rs...)
		}
		return refs, true
	case *hclsyntax.ConditionalExpr:
		cond, ok := x.Condition.(*hclsyntax.BinaryOpExpr)
		if !ok || cond.Op != hclsyntax.OpNotEqual || !isNull(cond.RHS) {
			return nil, false
		}
		l, lok := forwardRefs(cond.LHS, eachOK)
		t, tok := forwardRefs(x.TrueResult, eachOK)
		f, fok := forwardRefs(x.FalseResult, eachOK)
		if !lok || !tok || !fok || len(l) != 1 || len(t) != 1 || !sameRef(l[0], t[0]) {
			return nil, false
		}
		return append(t, f...), true
	}
	return nil, false
}

func isNull(e hclsyntax.Expression) bool {
	lit, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && lit.Val.IsNull()
}

func sameRef(a, b passRef) bool {
	return a.each == b.each && a.name == b.name && slices.Equal(a.path, b.path)
}

// traversalRef reads var.<name>.<attr>... or each.value.<attr>...; an index
// step is not an attribute path.
func traversalRef(t hcl.Traversal, eachOK bool) (passRef, bool) {
	var names []string
	for _, step := range t[1:] {
		a, ok := step.(hcl.TraverseAttr)
		if !ok {
			return passRef{}, false
		}
		names = append(names, a.Name)
	}
	switch {
	case t.RootName() == "var" && len(names) >= 1:
		return passRef{name: names[0], path: names[1:]}, true
	case t.RootName() == "each" && eachOK && len(names) >= 1 && names[0] == "value":
		return passRef{each: true, path: names[1:]}, true
	}
	return passRef{}, false
}

// forEachVar returns the root variable whose elements a call's for_each map
// holds unchanged: var.<name>, { for k, v in var.<name> : k => v [if ...] },
// or a local holding either. "" when there is none.
func forEachVar(m *Module, file string, x *Expr, viaLocal bool) string {
	if x == nil {
		return ""
	}
	e := reparse(file, *x)
	switch t := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if n, ok := rootName(t.Traversal, "var"); ok {
			return n
		}
		if n, ok := rootName(t.Traversal, "local"); ok && !viaLocal {
			for _, l := range m.Locals {
				if l.Name == n {
					return forEachVar(m, l.File, &l.Value, true)
				}
			}
		}
	case *hclsyntax.ForExpr:
		coll, cok := t.CollExpr.(*hclsyntax.ScopeTraversalExpr)
		key, kok := t.KeyExpr.(*hclsyntax.ScopeTraversalExpr)
		val, vok := t.ValExpr.(*hclsyntax.ScopeTraversalExpr)
		if !cok || !kok || !vok || t.Group || t.KeyVar == "" || len(key.Traversal) != 1 || len(val.Traversal) != 1 ||
			key.Traversal.RootName() != t.KeyVar || val.Traversal.RootName() != t.ValVar {
			return ""
		}
		if n, ok := rootName(coll.Traversal, "var"); ok {
			return n
		}
	}
	return ""
}

// rootName returns <name> of a traversal that is exactly <root>.<name>.
func rootName(t hcl.Traversal, root string) (string, bool) {
	if len(t) != 2 || t.RootName() != root {
		return "", false
	}
	a, ok := t[1].(hcl.TraverseAttr)
	return a.Name, ok
}

// passCheck collects the candidates of one area's calls for one rule.
type passCheck struct {
	a     *Area
	rule  string
	lc    *lineChanges
	seen  map[string]bool
	cands []Candidate
}

func (p *passCheck) add(rule, addr string, anchors []anchor, head *string) {
	if rule != p.rule {
		return
	}
	c := crossCandidate(p.a, p.lc, rule, addr, anchors, nil, head)
	k := fmt.Sprintf("%s:%d %s", c.File, *c.Line, c.Subject)
	if !p.seen[k] {
		p.seen[k] = true
		p.cands = append(p.cands, c)
	}
}

func passthrough(a *Area, rule string) Result {
	p := &passCheck{a: a, rule: rule, lc: &lineChanges{a: a, cache: map[string][]lineRange{}}, seen: map[string]bool{}}
	vars := map[string]*Variable{}
	for i := range a.Head.Variables {
		vars[a.Head.Variables[i].Name] = &a.Head.Variables[i]
	}
	for _, call := range a.Head.ModuleCalls {
		if callee := calledArea(a, call); callee != nil {
			p.call(call, callee, vars)
		}
	}
	return Result{Candidates: p.cands}
}

// call compares the arguments one call forwards with the callee's variables.
func (p *passCheck) call(call ModuleCall, callee *Area, vars map[string]*Variable) {
	subVars := map[string]*Variable{}
	for i := range callee.Head.Variables {
		subVars[callee.Head.Variables[i].Name] = &callee.Head.Variables[i]
	}
	eachVar := forEachVar(p.a.Head, call.File, call.Body.Arg("for_each"), false)
	set := map[string]bool{}
	var objects []string // root variables whose attributes the call forwards
	for _, at := range call.Body.Attributes {
		if moduleMetaAttrs[at.Name] {
			continue
		}
		set[at.Name] = true
		sv := subVars[at.Name]
		e := reparse(call.File, at.Value)
		if sv == nil || e == nil {
			continue
		}
		refs, ok := forwardRefs(e, eachVar != "")
		if !ok {
			continue
		}
		arg := anchor{areaFile(p.a.Path, call.File), at.Range.Start.Line}
		for _, r := range refs {
			if r.each {
				r.name = eachVar
			}
			rv := vars[r.name]
			if rv == nil {
				continue
			}
			if len(r.path) > 0 && !slices.Contains(objects, r.name) {
				objects = append(objects, r.name)
			}
			p.ref(r, rv, sv, callee, arg, len(refs) > 1)
		}
	}
	// A callee variable the call sets by no expression, while it forwards
	// attributes of a root object, is an attribute the root cannot pass.
	for _, name := range objects {
		rv := vars[name]
		for i := range callee.Head.Variables {
			sv := &callee.Head.Variables[i]
			if set[sv.Name] {
				continue
			}
			p.add("structure.passthrough-field-drift", "var."+name, []anchor{p.typeLine(rv), p.rootFile(rv, rv.Line), p.subFile(callee, sv, sv.Line)}, typeText(rv))
		}
	}
}

func (p *passCheck) rootFile(v *Variable, line int) anchor {
	return anchor{areaFile(p.a.Path, v.File), line}
}

func (p *passCheck) subFile(callee *Area, v *Variable, line int) anchor {
	return anchor{areaFile(callee.Path, v.File), line}
}

// typeText is the source text of a variable's type, or nil with none.
func typeText(v *Variable) *string {
	if x := v.Body.Arg("type"); x != nil {
		return &x.Text
	}
	return nil
}

// typeLine is the line of a variable's type argument, or of the variable.
func (p *passCheck) typeLine(v *Variable) anchor {
	if x := v.Body.Arg("type"); x != nil {
		return p.rootFile(v, x.Range.Start.Line)
	}
	return p.rootFile(v, v.Line)
}

// ref compares one forwarded reference with the callee variable it lands in.
func (p *passCheck) ref(r passRef, rv, sv *Variable, callee *Area, arg anchor, several bool) {
	const field, def = "structure.passthrough-field-drift", "structure.passthrough-default-drift"
	addr := strings.Join(append([]string{"var." + r.name}, r.path...), ".")
	root := varType(rv)
	if r.each {
		switch root.kind {
		case passColl:
			root = root.elem
		case passAny:
		default:
			// The elements of an object are not one type: undecidable.
			p.add(field, addr, []anchor{p.typeLine(rv), arg, p.subFile(callee, sv, sv.Line)}, typeText(rv))
			return
		}
	}
	var last *passAttr
	declared := []anchor{p.rootFile(rv, rv.Line)}
	if d := rv.Body.Arg("default"); d != nil && len(r.path) == 0 && !r.each {
		declared = append(declared, p.rootFile(rv, d.Range.Start.Line))
	}
	for _, step := range r.path {
		switch root.kind {
		case passObject:
			if last = root.attr(step); last == nil {
				return // not an attribute of the type: an invalid reference
			}
			root = last.typ
			declared = []anchor{p.rootFile(rv, last.line)}
		case passColl:
			last, root = nil, root.elem
		case passAny:
			last = nil
		default:
			return
		}
	}
	subDecl := []anchor{p.subFile(callee, sv, sv.Line)}
	if d := sv.Body.Arg("default"); d != nil {
		subDecl = append([]anchor{p.subFile(callee, sv, d.Range.Start.Line)}, subDecl...)
	}
	// Step 2 at the top: the unset values of the reference and the variable.
	if sv.Default != nil {
		rootUnset, known, required := p.refUnset(r, rv, last)
		nullable := sv.Nullable != nil && normExpr(sv.Nullable.Text) == "false"
		switch {
		case required:
		case !known || several || !sameUnset(rootUnset, sv.Default.Text, nullable):
			p.add(def, addr, slices.Concat(declared, []anchor{arg}, subDecl), &rootUnset)
		}
	}
	p.walk(root, varType(sv), addr, rv, sv, callee, arg)
}

// refUnset is what a reference yields when the caller sets nothing. required
// is set when it has no unset value; known is false when the engine cannot
// tell without evaluating the variable's default.
func (p *passCheck) refUnset(r passRef, rv *Variable, last *passAttr) (value string, known, required bool) {
	switch {
	case len(r.path) == 0 && r.each:
		return "", true, true
	case len(r.path) == 0 && rv.Default == nil:
		return "", true, true
	case len(r.path) == 0:
		return rv.Default.Text, true, false
	case r.each || (rv.Default != nil && len(r.path) == 1 && normExpr(rv.Default.Text) == "{ }"):
		if last == nil {
			return "", false, false
		}
		v, ok := last.unset()
		return v, true, !ok
	case rv.Default == nil:
		return "", true, true
	}
	return rv.Default.Text, false, false
}

// sameUnset compares two unset values as source text. A root null is no
// difference where Terraform substitutes the callee's default for it.
func sameUnset(root, sub string, substitutes bool) bool {
	if normExpr(root) == "null" && substitutes {
		return true
	}
	return normExpr(root) == normExpr(sub)
}

// walk compares the attributes of two types at every depth.
func (p *passCheck) walk(r, s *passType, addr string, rv, sv *Variable, callee *Area, arg anchor) {
	const field, def = "structure.passthrough-field-drift", "structure.passthrough-default-drift"
	switch {
	case r.kind == passColl && s.kind == passColl:
		p.walk(r.elem, s.elem, addr, rv, sv, callee, arg)
		return
	case r.kind != passObject || s.kind != passObject:
		return
	}
	for _, sa := range s.attrs {
		if r.attr(sa.name) == nil {
			p.add(field, addr, []anchor{p.rootFile(rv, r.line), arg, p.subFile(callee, sv, sa.line)}, typeText(rv))
		}
	}
	for _, ra := range r.attrs {
		sub := addr + "." + ra.name
		sa := s.attr(ra.name)
		if sa == nil {
			p.add(field, sub, []anchor{p.rootFile(rv, ra.line), arg, p.subFile(callee, sv, s.line)}, typeText(rv))
			continue
		}
		ru, rok := ra.unset()
		su, sok := sa.unset()
		if rok && sok && !sameUnset(ru, su, sa.def != nil) {
			p.add(def, sub, []anchor{p.rootFile(rv, ra.line), arg, p.subFile(callee, sv, sa.line)}, &ru)
		}
		p.walk(ra.typ, sa.typ, sub, rv, sv, callee, arg)
	}
}
