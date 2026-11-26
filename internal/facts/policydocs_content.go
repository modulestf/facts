package facts

// The content of a policy document: a data "aws_iam_policy_document" block,
// a jsonencode() object or a JSON string, each read into one normal form.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Policy is a document in normal form. Version and ID are null when the
// document does not set them; a data source sets Version to its default.
type Policy struct {
	Version    *PolicyValue      `json:"version"`
	ID         *PolicyValue      `json:"id"`
	Statements []PolicyStatement `json:"statements"`
}

// PolicyStatement is one statement. A list the statement does not set is
// null. Every list is sorted by its JSON text and keeps duplicates; repeated
// principals and condition blocks are not merged. Effect is Allow when a
// data source leaves it out, as the provider reads it.
type PolicyStatement struct {
	Sid           *PolicyValue      `json:"sid"`
	Effect        string            `json:"effect"`
	Actions       []PolicyValue     `json:"actions"`
	NotActions    []PolicyValue     `json:"not_actions"`
	Resources     []PolicyValue     `json:"resources"`
	NotResources  []PolicyValue     `json:"not_resources"`
	Principals    []PolicyPrincipal `json:"principals"`
	NotPrincipals []PolicyPrincipal `json:"not_principals"`
	Conditions    []PolicyCondition `json:"conditions"`
}

// PolicyPrincipal is one principals block, or one key of a JSON Principal.
// A JSON Principal of "*" is type "*" with identifier "*", as the provider
// writes a principals block of type "*".
type PolicyPrincipal struct {
	Type        string        `json:"type"`
	Identifiers []PolicyValue `json:"identifiers"`
}

// PolicyCondition is one condition block, or one operator and key of a JSON
// Condition.
type PolicyCondition struct {
	Test     string        `json:"test"`
	Variable PolicyValue   `json:"variable"`
	Values   []PolicyValue `json:"values"`
}

// PolicyValue is a string: plain when it is literal, else a template of
// literal parts and references. A number or a bool is its text, as IAM
// reads it.
type PolicyValue struct {
	Text     string
	Template []PolicyPart
}

// PolicyPart is a literal part or a reference of a template.
type PolicyPart struct {
	Text string
	Ref  string
}

func (v PolicyValue) MarshalJSON() ([]byte, error) {
	if v.Template == nil {
		return json.Marshal(v.Text)
	}
	return json.Marshal(struct {
		Template []PolicyPart `json:"template"`
	}{v.Template})
}

func (p PolicyPart) MarshalJSON() ([]byte, error) {
	if p.Ref == "" {
		return json.Marshal(p.Text)
	}
	return json.Marshal(struct {
		Ref string `json:"ref"`
	}{p.Ref})
}

// refs are the references of the value.
func (v PolicyValue) refs() []string {
	var out []string
	for _, p := range v.Template {
		if p.Ref != "" {
			out = append(out, p.Ref)
		}
	}
	return out
}

// contentError is a document the reader cannot decide.
type contentError struct{ code, detail string }

func (e contentError) Error() string { return e.code + ": " + e.detail }

func undecided(code, detail string) error { return contentError{code, detail} }

// reasonOf turns a content error into a reason.
func reasonOf(err error) *PolicyReason {
	if ce, ok := errors.AsType[contentError](err); ok {
		return &PolicyReason{Code: ce.code, Detail: ce.detail}
	}
	return &PolicyReason{Code: PolicyReasonOtherExpression, Detail: err.Error()}
}

// --- values written in HCL ---

// canonTraversal renders a traversal with literal index keys, or false for
// any other step.
func canonTraversal(t hcl.Traversal) (string, bool) {
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
				return "", false
			case s.Key.Type() == cty.String:
				b.WriteString("[" + strconv.Quote(s.Key.AsString()) + "]")
			case s.Key.Type() == cty.Number:
				b.WriteString("[" + s.Key.AsBigFloat().Text('f', -1) + "]")
			default:
				return "", false
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// hclValue reads a string value: a literal, a template of literal parts and
// plain references, or a plain reference.
func hclValue(e hclsyntax.Expression) (PolicyValue, error) {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return scalarText(x.Val)
	case *hclsyntax.TemplateWrapExpr:
		return hclValue(x.Wrapped)
	case *hclsyntax.ScopeTraversalExpr:
		s, ok := canonTraversal(x.Traversal)
		if !ok {
			return PolicyValue{}, undecided(PolicyReasonNotLiteral, "a reference with a dynamic index")
		}
		return PolicyValue{Template: []PolicyPart{{Ref: s}}}, nil
	case *hclsyntax.TemplateExpr:
		var parts []PolicyPart
		literal := true
		for _, p := range x.Parts {
			switch q := p.(type) {
			case *hclsyntax.LiteralValueExpr:
				v, err := scalarText(q.Val)
				if err != nil {
					return PolicyValue{}, err
				}
				if n := len(parts); n > 0 && parts[n-1].Ref == "" {
					parts[n-1].Text += v.Text
				} else {
					parts = append(parts, PolicyPart{Text: v.Text})
				}
			case *hclsyntax.ScopeTraversalExpr:
				s, ok := canonTraversal(q.Traversal)
				if !ok {
					return PolicyValue{}, undecided(PolicyReasonNotLiteral, "a reference with a dynamic index")
				}
				literal = false
				parts = append(parts, PolicyPart{Ref: s})
			default:
				return PolicyValue{}, undecided(PolicyReasonNotLiteral, "an interpolation other than a plain reference")
			}
		}
		if literal {
			var b strings.Builder
			for _, p := range parts {
				b.WriteString(p.Text)
			}
			return PolicyValue{Text: b.String()}, nil
		}
		return PolicyValue{Template: parts}, nil
	}
	return PolicyValue{}, undecided(PolicyReasonNotLiteral, "an expression other than a string")
}

// scalarText is the text of a literal string, number or bool.
func scalarText(v cty.Value) (PolicyValue, error) {
	switch {
	case v.IsNull() || !v.IsKnown():
		return PolicyValue{}, undecided(PolicyReasonNotLiteral, "a null value")
	case v.Type() == cty.String:
		return PolicyValue{Text: v.AsString()}, nil
	case v.Type() == cty.Number:
		return PolicyValue{Text: v.AsBigFloat().Text('f', -1)}, nil
	case v.Type() == cty.Bool:
		return PolicyValue{Text: strconv.FormatBool(v.True())}, nil
	}
	return PolicyValue{}, undecided(PolicyReasonNotLiteral, "a value that is not a string")
}

// hclList reads a literal list of string values, sorted.
func hclList(e hclsyntax.Expression) ([]PolicyValue, error) {
	tup, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, undecided(PolicyReasonNotLiteral, "a list that is not written out")
	}
	if len(tup.Exprs) > maxPolicyValues {
		return nil, undecided(PolicyReasonTooLarge, "a list over "+strconv.Itoa(maxPolicyValues)+" values")
	}
	out := make([]PolicyValue, 0, len(tup.Exprs))
	for _, x := range tup.Exprs {
		v, err := hclValue(x)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	sortValues(out)
	return out, nil
}

func sortValues(vs []PolicyValue) {
	slices.SortStableFunc(vs, func(a, b PolicyValue) int { return strings.Compare(canonJSON(a), canonJSON(b)) })
}

// hclLiteralString reads a string with no interpolation.
func hclLiteralString(e hclsyntax.Expression, what string) (string, error) {
	v, err := hclValue(e)
	if err != nil {
		return "", err
	}
	if v.Template != nil {
		return "", undecided(PolicyReasonNotLiteral, what+" is not a literal")
	}
	return v.Text, nil
}

// --- a data "aws_iam_policy_document" block ---

// policyDataMeta are the arguments of the data block that do not change its
// content: count and for_each are its gate.
var policyDataMeta = map[string]bool{"count": true, "for_each": true, "depends_on": true, "provider": true}

// dataSourcePolicy reads the content of a data block. A merging document
// is undecidable: the provider merges by statement id, and the reader does
// not repeat that.
func dataSourcePolicy(b *hclsyntax.Block) (*Policy, error) {
	p := &Policy{Version: &PolicyValue{Text: "2012-10-17"}, Statements: []PolicyStatement{}}
	for _, a := range sortedAttrs(b.Body) {
		switch {
		case policyDataMeta[a.Name]:
		case a.Name == "source_policy_documents" || a.Name == "override_policy_documents":
			return nil, undecided(PolicyReasonMerge, a.Name)
		case a.Name == "source_json" || a.Name == "override_json":
			return nil, undecided(PolicyReasonMerge, a.Name)
		case a.Name == "version":
			v, err := hclValue(a.Expr)
			if err != nil {
				return nil, err
			}
			p.Version = &v
		case a.Name == "policy_id":
			v, err := hclValue(a.Expr)
			if err != nil {
				return nil, err
			}
			p.ID = &v
		default:
			return nil, undecided(PolicyReasonUnsupportedArg, a.Name)
		}
	}
	for _, nb := range b.Body.Blocks {
		switch nb.Type {
		case "lifecycle":
		case "statement":
			if len(p.Statements) >= maxPolicyStatements {
				return nil, undecided(PolicyReasonTooLarge, "over "+strconv.Itoa(maxPolicyStatements)+" statements")
			}
			s, err := hclStatement(nb)
			if err != nil {
				return nil, err
			}
			p.Statements = append(p.Statements, s)
		case "dynamic":
			return nil, undecided(PolicyReasonDynamicBlock, strings.Join(nb.Labels, " "))
		default:
			return nil, undecided(PolicyReasonUnsupportedBlock, nb.Type)
		}
	}
	return p, nil
}

func hclStatement(b *hclsyntax.Block) (PolicyStatement, error) {
	s := PolicyStatement{Effect: "Allow"}
	if len(b.Labels) != 0 {
		return s, undecided(PolicyReasonUnsupportedBlock, "statement with a label")
	}
	for _, a := range sortedAttrs(b.Body) {
		var err error
		switch a.Name {
		case "sid":
			var v PolicyValue
			v, err = hclValue(a.Expr)
			s.Sid = &v
		case "effect":
			var t string
			if t, err = hclLiteralString(a.Expr, "effect"); err == nil && t != "Allow" && t != "Deny" {
				err = undecided(PolicyReasonInvalidEffect, t)
			}
			s.Effect = t
		case "actions":
			s.Actions, err = hclList(a.Expr)
		case "not_actions":
			s.NotActions, err = hclList(a.Expr)
		case "resources":
			s.Resources, err = hclList(a.Expr)
		case "not_resources":
			s.NotResources, err = hclList(a.Expr)
		default:
			err = undecided(PolicyReasonUnsupportedArg, "statement."+a.Name)
		}
		if err != nil {
			return s, err
		}
	}
	for _, nb := range b.Body.Blocks {
		switch nb.Type {
		case "principals", "not_principals":
			pr, err := hclPrincipal(nb)
			if err != nil {
				return s, err
			}
			if nb.Type == "principals" {
				s.Principals = append(s.Principals, pr)
			} else {
				s.NotPrincipals = append(s.NotPrincipals, pr)
			}
		case "condition":
			c, err := hclCondition(nb)
			if err != nil {
				return s, err
			}
			s.Conditions = append(s.Conditions, c)
		case "dynamic":
			return s, undecided(PolicyReasonDynamicBlock, strings.Join(nb.Labels, " "))
		default:
			return s, undecided(PolicyReasonUnsupportedBlock, "statement."+nb.Type)
		}
	}
	sortStatementParts(&s)
	return s, nil
}

func hclPrincipal(b *hclsyntax.Block) (PolicyPrincipal, error) {
	var pr PolicyPrincipal
	seen := map[string]bool{}
	for _, a := range sortedAttrs(b.Body) {
		var err error
		switch a.Name {
		case "type":
			pr.Type, err = hclLiteralString(a.Expr, "principal type")
		case "identifiers":
			pr.Identifiers, err = hclList(a.Expr)
		default:
			err = undecided(PolicyReasonUnsupportedArg, b.Type+"."+a.Name)
		}
		if err != nil {
			return pr, err
		}
		seen[a.Name] = true
	}
	if len(b.Body.Blocks) > 0 || !seen["type"] || !seen["identifiers"] {
		return pr, undecided(PolicyReasonUnsupportedBlock, b.Type+" without type and identifiers")
	}
	return pr, nil
}

func hclCondition(b *hclsyntax.Block) (PolicyCondition, error) {
	var c PolicyCondition
	seen := map[string]bool{}
	for _, a := range sortedAttrs(b.Body) {
		var err error
		switch a.Name {
		case "test":
			c.Test, err = hclLiteralString(a.Expr, "condition test")
		case "variable":
			c.Variable, err = hclValue(a.Expr)
		case "values":
			c.Values, err = hclList(a.Expr)
		default:
			err = undecided(PolicyReasonUnsupportedArg, "condition."+a.Name)
		}
		if err != nil {
			return c, err
		}
		seen[a.Name] = true
	}
	if len(b.Body.Blocks) > 0 || !seen["test"] || !seen["variable"] || !seen["values"] {
		return c, undecided(PolicyReasonUnsupportedBlock, "condition without test, variable and values")
	}
	return c, nil
}

// sortStatementParts sorts the principals and conditions of a statement by
// their JSON text, so their order in the source does not matter.
func sortStatementParts(s *PolicyStatement) {
	for _, ps := range []*[]PolicyPrincipal{&s.Principals, &s.NotPrincipals} {
		slices.SortStableFunc(*ps, func(a, b PolicyPrincipal) int { return strings.Compare(canonJSON(a), canonJSON(b)) })
	}
	slices.SortStableFunc(s.Conditions, func(a, b PolicyCondition) int { return strings.Compare(canonJSON(a), canonJSON(b)) })
}

// --- documents written as JSON ---

// jnode is a JSON value read from HCL or from text: an object with its keys
// in order, a list, or a scalar as a PolicyValue. A null is a nil scalar.
type jnode struct {
	obj  []jfield
	list []jnode
	val  *PolicyValue
	kind byte // 'o', 'l', 's' or 'n' for null
}

type jfield struct {
	key string
	val jnode
}

// hclJSON reads the argument of a jsonencode() call.
func hclJSON(e hclsyntax.Expression, depth int) (jnode, error) {
	if depth > 16 {
		return jnode{}, undecided(PolicyReasonTooLarge, "nested too deep")
	}
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return hclJSON(x.Expression, depth)
	case *hclsyntax.ObjectConsExpr:
		n := jnode{kind: 'o'}
		seen := map[string]bool{}
		for _, it := range x.Items {
			k := keyName(it.KeyExpr)
			if k == "" {
				return jnode{}, undecided(PolicyReasonNotLiteral, "an object key that is not a name or a string")
			}
			if seen[k] {
				return jnode{}, undecided(PolicyReasonInvalidJSON, "key "+k+" twice")
			}
			seen[k] = true
			v, err := hclJSON(it.ValueExpr, depth+1)
			if err != nil {
				return jnode{}, err
			}
			n.obj = append(n.obj, jfield{k, v})
		}
		return n, nil
	case *hclsyntax.TupleConsExpr:
		if len(x.Exprs) > maxPolicyValues {
			return jnode{}, undecided(PolicyReasonTooLarge, "a list over "+strconv.Itoa(maxPolicyValues)+" values")
		}
		n := jnode{kind: 'l', list: []jnode{}}
		for _, el := range x.Exprs {
			v, err := hclJSON(el, depth+1)
			if err != nil {
				return jnode{}, err
			}
			n.list = append(n.list, v)
		}
		return n, nil
	case *hclsyntax.LiteralValueExpr:
		if x.Val.IsNull() {
			return jnode{kind: 'n'}, nil
		}
	}
	v, err := hclValue(e)
	if err != nil {
		return jnode{}, err
	}
	return jnode{kind: 's', val: &v}, nil
}

// textJSON reads a JSON document from text. A repeated key is invalid, as
// encoding/json would keep the last one silently.
func textJSON(text string) (jnode, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	n, err := readJSON(dec, 0)
	if err != nil {
		return jnode{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return jnode{}, undecided(PolicyReasonInvalidJSON, "text after the document")
	}
	return n, nil
}

func readJSON(dec *json.Decoder, depth int) (jnode, error) {
	if depth > 16 {
		return jnode{}, undecided(PolicyReasonTooLarge, "nested too deep")
	}
	tok, err := dec.Token()
	if err != nil {
		return jnode{}, undecided(PolicyReasonInvalidJSON, "does not parse")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			n := jnode{kind: 'o'}
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				k, ok := kt.(string)
				if err != nil || !ok {
					return jnode{}, undecided(PolicyReasonInvalidJSON, "does not parse")
				}
				if seen[k] {
					return jnode{}, undecided(PolicyReasonInvalidJSON, "key "+k+" twice")
				}
				seen[k] = true
				v, err := readJSON(dec, depth+1)
				if err != nil {
					return jnode{}, err
				}
				n.obj = append(n.obj, jfield{k, v})
			}
			_, err := dec.Token()
			if err != nil {
				return jnode{}, undecided(PolicyReasonInvalidJSON, "does not parse")
			}
			return n, nil
		case '[':
			n := jnode{kind: 'l', list: []jnode{}}
			for dec.More() {
				if len(n.list) >= maxPolicyValues {
					return jnode{}, undecided(PolicyReasonTooLarge, "a list over "+strconv.Itoa(maxPolicyValues)+" values")
				}
				v, err := readJSON(dec, depth+1)
				if err != nil {
					return jnode{}, err
				}
				n.list = append(n.list, v)
			}
			if _, err := dec.Token(); err != nil {
				return jnode{}, undecided(PolicyReasonInvalidJSON, "does not parse")
			}
			return n, nil
		}
	case string:
		return jnode{kind: 's', val: &PolicyValue{Text: t}}, nil
	case json.Number:
		return jnode{kind: 's', val: &PolicyValue{Text: t.String()}}, nil
	case bool:
		return jnode{kind: 's', val: &PolicyValue{Text: strconv.FormatBool(t)}}, nil
	case nil:
		return jnode{kind: 'n'}, nil
	}
	return jnode{}, undecided(PolicyReasonInvalidJSON, "does not parse")
}

// hasStatementKey reports a JSON object with a top-level Statement key.
func (n jnode) hasStatementKey() bool {
	if n.kind != 'o' {
		return false
	}
	return slices.ContainsFunc(n.obj, func(f jfield) bool { return f.key == "Statement" })
}

// jsonPolicy reads an IAM JSON policy document.
func jsonPolicy(n jnode) (*Policy, error) {
	if n.kind != 'o' {
		return nil, undecided(PolicyReasonInvalidJSON, "not an object")
	}
	p := &Policy{Statements: []PolicyStatement{}}
	var stmts []jnode
	for _, f := range n.obj {
		switch f.key {
		case "Version", "Id":
			if f.val.kind != 's' {
				return nil, undecided(PolicyReasonNotLiteral, f.key+" is not a string")
			}
			if f.key == "Version" {
				p.Version = f.val.val
			} else {
				p.ID = f.val.val
			}
		case "Statement":
			switch f.val.kind {
			case 'o':
				stmts = []jnode{f.val}
			case 'l':
				stmts = f.val.list
			default:
				return nil, undecided(PolicyReasonInvalidJSON, "Statement is not an object or a list")
			}
		default:
			return nil, undecided(PolicyReasonUnsupportedKey, f.key)
		}
	}
	if len(stmts) > maxPolicyStatements {
		return nil, undecided(PolicyReasonTooLarge, "over "+strconv.Itoa(maxPolicyStatements)+" statements")
	}
	for _, sn := range stmts {
		s, err := jsonStatement(sn)
		if err != nil {
			return nil, err
		}
		p.Statements = append(p.Statements, s)
	}
	return p, nil
}

func jsonStatement(n jnode) (PolicyStatement, error) {
	var s PolicyStatement
	if n.kind != 'o' {
		return s, undecided(PolicyReasonInvalidJSON, "a statement that is not an object")
	}
	for _, f := range n.obj {
		var err error
		switch f.key {
		case "Sid":
			if f.val.kind != 's' {
				return s, undecided(PolicyReasonNotLiteral, "Sid is not a string")
			}
			s.Sid = f.val.val
		case "Effect":
			if f.val.kind != 's' || f.val.val.Template != nil || (f.val.val.Text != "Allow" && f.val.val.Text != "Deny") {
				return s, undecided(PolicyReasonInvalidEffect, "Effect is not Allow or Deny")
			}
			s.Effect = f.val.val.Text
		case "Action":
			s.Actions, err = jsonList(f.val, f.key)
		case "NotAction":
			s.NotActions, err = jsonList(f.val, f.key)
		case "Resource":
			s.Resources, err = jsonList(f.val, f.key)
		case "NotResource":
			s.NotResources, err = jsonList(f.val, f.key)
		case "Principal":
			s.Principals, err = jsonPrincipals(f.val)
		case "NotPrincipal":
			s.NotPrincipals, err = jsonPrincipals(f.val)
		case "Condition":
			s.Conditions, err = jsonConditions(f.val)
		default:
			err = undecided(PolicyReasonUnsupportedKey, "Statement."+f.key)
		}
		if err != nil {
			return s, err
		}
	}
	if s.Effect == "" {
		return s, undecided(PolicyReasonInvalidEffect, "a statement with no Effect")
	}
	sortStatementParts(&s)
	return s, nil
}

// jsonList reads a string or a list of strings, sorted.
func jsonList(n jnode, what string) ([]PolicyValue, error) {
	switch n.kind {
	case 's':
		return []PolicyValue{*n.val}, nil
	case 'l':
		out := make([]PolicyValue, 0, len(n.list))
		for _, el := range n.list {
			if el.kind != 's' {
				return nil, undecided(PolicyReasonNotLiteral, what+" holds a value that is not a string")
			}
			out = append(out, *el.val)
		}
		sortValues(out)
		return out, nil
	}
	return nil, undecided(PolicyReasonNotLiteral, what+" is not a string or a list")
}

func jsonPrincipals(n jnode) ([]PolicyPrincipal, error) {
	if n.kind == 's' && n.val.Template == nil && n.val.Text == "*" {
		return []PolicyPrincipal{{Type: "*", Identifiers: []PolicyValue{{Text: "*"}}}}, nil
	}
	if n.kind != 'o' {
		return nil, undecided(PolicyReasonNotLiteral, "Principal is not \"*\" or an object")
	}
	var out []PolicyPrincipal
	for _, f := range n.obj {
		ids, err := jsonList(f.val, "Principal."+f.key)
		if err != nil {
			return nil, err
		}
		out = append(out, PolicyPrincipal{Type: f.key, Identifiers: ids})
	}
	return out, nil
}

func jsonConditions(n jnode) ([]PolicyCondition, error) {
	if n.kind != 'o' {
		return nil, undecided(PolicyReasonNotLiteral, "Condition is not an object")
	}
	var out []PolicyCondition
	for _, op := range n.obj {
		if op.val.kind != 'o' {
			return nil, undecided(PolicyReasonNotLiteral, "Condition."+op.key+" is not an object")
		}
		for _, kv := range op.val.obj {
			vals, err := jsonList(kv.val, "Condition."+op.key+"."+kv.key)
			if err != nil {
				return nil, err
			}
			out = append(out, PolicyCondition{Test: op.key, Variable: PolicyValue{Text: kv.key}, Values: vals})
		}
	}
	return out, nil
}

// policyRefs are the references the content holds, sorted and unique.
func policyRefs(p *Policy) []string {
	var out []string
	add := func(vs ...PolicyValue) {
		for _, v := range vs {
			out = append(out, v.refs()...)
		}
	}
	if p.Version != nil {
		add(*p.Version)
	}
	if p.ID != nil {
		add(*p.ID)
	}
	for _, s := range p.Statements {
		if s.Sid != nil {
			add(*s.Sid)
		}
		add(s.Actions...)
		add(s.NotActions...)
		add(s.Resources...)
		add(s.NotResources...)
		for _, pr := range slices.Concat(s.Principals, s.NotPrincipals) {
			add(pr.Identifiers...)
		}
		for _, c := range s.Conditions {
			add(c.Variable)
			add(c.Values...)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// jsonStringDoc reports whether a string expression holds a JSON policy: a
// literal whose JSON has a Statement key, or a template whose literal text
// names one. Text that is no JSON object is no document.
func jsonStringDoc(e hclsyntax.Expression) (text string, interpolated, ok bool) {
	t, isTemplate := e.(*hclsyntax.TemplateExpr)
	if !isTemplate {
		return "", false, false
	}
	var lit bytes.Buffer
	for _, p := range t.Parts {
		if l, ok := p.(*hclsyntax.LiteralValueExpr); ok && l.Val.Type() == cty.String && !l.Val.IsNull() {
			lit.WriteString(l.Val.AsString())
		} else {
			interpolated = true
		}
	}
	s := lit.String()
	if !strings.Contains(s, `"Statement"`) {
		return "", false, false
	}
	if interpolated {
		return s, true, true
	}
	n, err := textJSON(s)
	if err != nil {
		return s, false, strings.HasPrefix(strings.TrimSpace(s), "{")
	}
	return s, false, n.hasStatementKey()
}
