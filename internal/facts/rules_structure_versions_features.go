package facts

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/modulestf/facts/data"
)

// The detector of structure.feature-above-version-floor: the core feature
// table, the walk over a module and the check.

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
