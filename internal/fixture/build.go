package fixture

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BuildSchemaVersion is the schema_version of the fixture-build result,
// schema/fixture-build.schema.json.
const BuildSchemaVersion = 1

// Limits of the builder's inputs.
const (
	maxTriggerBytes  = 4 << 20
	maxTemplateBytes = 256 << 10
	maxExprBytes     = 16384
	maxCases         = 32
	maxReasonChars   = 200
	maxRefTextBytes  = 256
)

// BuildOptions are the inputs of one fixture build.
type BuildOptions struct {
	Trigger          []byte // one trigger of a behaviour report, with the classifier's cases
	Template         []byte // the L1 and L2 template: "#-- <name>" sections
	ResourceTemplate []byte // L3: the resource template, whose sections add to and replace the L1 ones
	Level            string // L1, L2 or L3
	Attribute        string // L2: count or for_each; L3: the attribute under test
	Resource         string // L3: the resource type under test
}

// File is one file of a built fixture, by its slash path under the output
// directory.
type File struct {
	Path string
	Data []byte
}

// Built is the result of a build: Result, the object the command prints,
// and the files of the fixture, none when the build was refused.
type Built struct {
	OK     bool
	Result map[string]any
	Files  []File
}

// refusal is a trigger the builder cannot model, with its fixed reason.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

func refuse(reason string) error {
	if utf8.RuneCountInString(reason) > maxReasonChars {
		reason = string([]rune(reason)[:maxReasonChars])
	}
	return refusal{reason}
}

// errBadLevel is a level other than L1, L2 and L3.
var errBadLevel = errors.New("the level is not L1, L2 or L3")

var (
	inputName   = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_-]{0,63}\z`)
	resourceRef = regexp.MustCompile(`\A(?:data\.)?[a-z][a-z0-9_]*\.[A-Za-z_][A-Za-z0-9_-]*(\[\*\]|\[[0-9]+\]|\.\*)?\.([A-Za-z_][A-Za-z0-9_]*)\z`)
	defaultVar  = regexp.MustCompile(`\Avar\.([A-Za-z_][A-Za-z0-9_-]{0,63})\z`)
	caseID      = regexp.MustCompile(`\A[A-Za-z0-9_.:-]{1,64}\z`)
	elementKey  = regexp.MustCompile(`\A[A-Za-z0-9_.-]{1,64}\z`)
	smallIndex  = regexp.MustCompile(`\A[0-9]\z`)
	eachKey     = regexp.MustCompile(`\A[A-Za-z0-9_.-]{0,64}\z`)
	sectionHead = regexp.MustCompile(`\A#-- ([^\t\n\v\f\r ]+)\n\z`)
	placeholder = regexp.MustCompile(`__([A-Z]+)__`)
)

// input is one value the expression reads: a variable or a resource or
// data source attribute, which the fixture turns into a local.
type input struct {
	name string
	typ  valueType
	text string // the text the expression names it by
}

// Build builds the fixture pair of one trigger: per side with an
// expression, main.tf with a local per input whose state a case var file
// picks, terraform_data stand-ins whose output is a typed unknown with a
// canary each, and subject.tf with the expression; a manifest per side for
// fixture-check; a var file per case. A trigger the builder cannot model is
// refused with a fixed reason and no file. An error is bad input: a trigger
// that is not one JSON object, or a template that lacks a section or a
// value.
func Build(o BuildOptions) (*Built, error) {
	switch o.Level {
	case "L1", "L2", "L3":
	default:
		return nil, errBadLevel
	}
	v, err := decodeJSON(o.Trigger)
	if err != nil {
		return nil, fmt.Errorf("trigger: %w", err)
	}
	t, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("trigger: not a JSON object")
	}
	b := &builder{o: o, t: t, seen: map[string]bool{}}
	if err := b.sections(); err != nil {
		return nil, err
	}
	res, err := b.build()
	if r, ok := errors.AsType[refusal](err); ok {
		return &Built{Result: map[string]any{"ok": false, "reason": r.reason, "schema_version": BuildSchemaVersion}}, nil
	}
	if err != nil {
		return nil, err
	}
	res["ok"], res["schema_version"] = true, BuildSchemaVersion
	return &Built{OK: true, Result: res, Files: b.files}, nil
}

type builder struct {
	o     BuildOptions
	t     map[string]any
	sec   map[string]string
	in    []input
	seen  map[string]bool
	files []File
}

// sections reads the templates' sections: each starts at a line "#-- <name>";
// text before the first is a comment. A later section of the same name
// replaces an earlier one, and the resource template's come after the L1
// template's.
func (b *builder) sections() error {
	b.sec = map[string]string{}
	tmpls := [][]byte{b.o.Template}
	if b.o.Level == "L3" {
		tmpls = append(tmpls, b.o.ResourceTemplate)
	}
	for _, tm := range tmpls {
		if len(tm) > maxTemplateBytes {
			return fmt.Errorf("a template is over %d bytes", maxTemplateBytes)
		}
		cur, in := "", false
		for _, line := range strings.SplitAfter(string(tm), "\n") {
			if m := sectionHead.FindStringSubmatch(line); m != nil {
				cur, in = m[1], true
				b.sec[cur] = ""
				continue
			}
			if in {
				b.sec[cur] += line
			}
		}
	}
	return nil
}

// fill is a section with each __NAME__ replaced, in one pass, by its value.
func (b *builder) fill(name string, vals map[string]string) (string, error) {
	s, ok := b.sec[name]
	if !ok {
		return "", fmt.Errorf("template: no section %s", name)
	}
	var missing string
	out := placeholder.ReplaceAllStringFunc(s, func(m string) string {
		k := m[2 : len(m)-2]
		v, ok := vals[k]
		if !ok && missing == "" {
			missing = k
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("template: no value %s in section %s", missing, name)
	}
	return out, nil
}

func (b *builder) addInput(name string, typ valueType, text string) {
	if b.seen[name] {
		return
	}
	b.seen[name] = true
	b.in = append(b.in, input{name: name, typ: typ, text: text})
}

// fixtureCase is one case of the classifier, ready to write.
type fixtureCase struct {
	id      any
	idText  string
	vars    map[string]any
	inputs  any
	addr    string
	unknown []string
}

func (b *builder) build() (map[string]any, error) {
	sides, exprs, err := b.expressions()
	if err != nil {
		return nil, err
	}
	if err := b.referencedInputs(); err != nil {
		return nil, err
	}
	wrap, err := b.contextualRefs()
	if err != nil {
		return nil, err
	}
	omit, hasOmit := "", false
	if cat, _ := str(b.t["category"]); cat == "default-change" && b.t["category"] != nil {
		if a, ok := str(b.t["address"]); ok {
			if m := defaultVar.FindStringSubmatch(a); m != nil {
				omit, hasOmit = m[1], true
				if !b.seen[omit] {
					return nil, refuse("the variable of a default change is not a referenced input")
				}
			}
		}
	}
	cases, err := b.cases(wrap, omit, hasOmit)
	if err != nil {
		return nil, err
	}
	main, blocks, canaries, err := b.mainTF(wrap, hasOmit)
	if err != nil {
		return nil, err
	}
	for _, c := range cases {
		data, err := encodeCanonical(c.vars)
		if err != nil {
			return nil, err
		}
		b.files = append(b.files, File{Path: "vars/" + c.idText + ".tfvars.json", Data: data})
	}
	if err := b.subjects(sides, exprs, wrap, omit, hasOmit, main, blocks); err != nil {
		return nil, err
	}
	outCases := make([]any, 0, len(cases))
	for _, c := range cases {
		outCases = append(outCases, map[string]any{"id": c.id, "inputs": c.inputs, "unknown_inputs": c.unknown, "addr": c.addr})
	}
	return map[string]any{"wrap": wrap, "sides": sides, "canaries": canaries, "cases": outCases}, nil
}

// expressions are the base and head expressions, and the sides that have
// one.
func (b *builder) expressions() ([]string, map[string]string, error) {
	exprs := map[string]string{}
	for _, s := range []string{"base", "head"} {
		v := b.t[s+"_expr"]
		if v != nil && !scalar(v) {
			return nil, nil, refuse("an expression is not a string")
		}
		if e, ok := str(v); ok {
			exprs[s] = e
		}
	}
	sides := []string{}
	for _, s := range []string{"base", "head"} {
		if exprs[s] != "" {
			sides = append(sides, s)
		}
	}
	if len(sides) == 0 {
		return nil, nil, refuse("the trigger has no expression")
	}
	for _, s := range sides {
		if len(exprs[s]) > maxExprBytes {
			return nil, nil, refuse("an expression is over 16 KiB")
		}
	}
	return sides, exprs, nil
}

func (b *builder) referencedInputs() error {
	refs, _ := b.t["referenced_inputs"].([]any)
	for _, rv := range refs {
		r, ok := rv.(map[string]any)
		name, isStr := str(r["name"])
		if !ok || !isStr || !inputName.MatchString(name) {
			return refuse("a referenced input has no name")
		}
		y, ok := parseType(r["type"])
		if !ok {
			return refuse("an input type is outside the L1 type grammar")
		}
		b.addInput(name, y, "var."+name)
	}
	return nil
}

// contextualRefs adds the resource and data source references as inputs
// and returns the wrap: count, for_each or none.
func (b *builder) contextualRefs() (string, error) {
	refs, _ := b.t["contextual_ref_details"].([]any)
	if len(refs) == 0 {
		if cr, ok := b.t["contextual_refs"].([]any); ok && len(cr) > 0 {
			return "", refuse("contextual references without details")
		}
	}
	wrap := "none"
	for _, rv := range refs {
		r, ok := rv.(map[string]any)
		x, xok := str(r["text"])
		k, kok := str(r["kind"])
		if !ok || !xok || !kok || len(x) > maxRefTextBytes {
			return "", refuse("a contextual reference has no text and kind")
		}
		switch k {
		case "resource", "data":
			m := resourceRef.FindStringSubmatch(x)
			if m == nil {
				return "", refuse("a resource reference shape L1 cannot model")
			}
			var y valueType
			var have bool
			if r["type"] != nil {
				y, have = parseType(r["type"])
			} else if m[2] == "id" {
				y, have = valueType{kind: "string"}, true
			}
			if !have {
				return "", refuse("a resource reference with no attribute type")
			}
			if strings.Contains(m[1], "*") {
				el := y
				y = valueType{kind: "list", elem: &el}
			}
			b.addInput(x, y, x)
		case "count":
			if x != "count.index" {
				return "", refuse("a count or each reference L1 cannot model")
			}
			if wrap == "for_each" {
				return "", refuse("count and each in one expression")
			}
			wrap = "count"
		case "each":
			if x != "each.key" {
				return "", refuse("an each.value reference is unverifiable at L1")
			}
			if wrap == "count" {
				return "", refuse("count and each in one expression")
			}
			wrap = "for_each"
		case "path", "terraform":
			return "", refuse("path and terraform references are unverifiable at L1")
		default:
			return "", refuse("local, module, self and other references are unverifiable at L1")
		}
	}
	return wrap, nil
}

var stateKeys = []string{"unknown", "null", "omitted"}

// cases reads the classifier's cases: each names exactly the inputs, each
// input in one state.
func (b *builder) cases(wrap, omit string, hasOmit bool) ([]fixtureCase, error) {
	cs, ok := b.t["cases"].([]any)
	if !ok || len(cs) == 0 {
		return nil, refuse("no cases")
	}
	if len(cs) > maxCases {
		return nil, refuse("over 32 cases")
	}
	winput := ""
	switch wrap {
	case "count":
		winput = "count.index"
	case "for_each":
		winput = "each.key"
	}
	names := make([]string, 0, len(b.in)+1)
	for _, p := range b.in {
		names = append(names, p.name)
	}
	if winput != "" {
		names = append(names, winput)
	}
	slices.Sort(names)
	want := strings.Join(names, "\n")
	ids, folded := map[string]bool{}, map[string]bool{}
	out := make([]fixtureCase, 0, len(cs))
	for _, cv := range cs {
		c, ok := cv.(map[string]any)
		id, idok := str(c["id"])
		inputs, inok := c["inputs"].(map[string]any)
		if !ok || !idok || !caseID.MatchString(id) || !inok {
			return nil, refuse("a case has no id or inputs")
		}
		if ids[id] {
			return nil, refuse("two cases share an id")
		}
		ids[id] = true
		// Each case is a var file named by its id, so two ids that differ only
		// in letter case would be one file on a case-insensitive file system:
		// refused on every file system alike.
		if folded[strings.ToLower(id)] {
			return nil, refuse("two case ids differ only in letter case")
		}
		folded[strings.ToLower(id)] = true
		keys := make([]string, 0, len(inputs))
		for k := range inputs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if strings.Join(keys, "\n") != want {
			return nil, refuse("a case does not name exactly the inputs")
		}
		fc, err := b.caseVars(inputs, omit, hasOmit)
		if err != nil {
			return nil, err
		}
		fc.id, fc.idText, fc.inputs = c["id"], id, c["inputs"]
		if winput != "" {
			if err := wrapInput(&fc, wrap, inputs[winput]); err != nil {
				return nil, err
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

func (b *builder) caseVars(inputs map[string]any, omit string, hasOmit bool) (fixtureCase, error) {
	fc := fixtureCase{vars: map[string]any{}, unknown: []string{}}
	for i, p := range b.in {
		s, ok := inputs[p.name].(map[string]any)
		if !ok {
			return fc, refuse("a case input has another shape")
		}
		var on []string
		for _, k := range stateKeys {
			if truthy(s[k]) {
				on = append(on, k)
			}
		}
		ue, _ := s["unknown_elements"].([]any)
		if len(on) > 1 || (len(on) > 0 && len(ue) > 0) {
			return fc, refuse("a case input has more than one state")
		}
		mode := "known"
		if len(on) > 0 {
			mode = on[0]
		}
		isOmit := hasOmit && p.name == omit
		if mode == "omitted" && !isOmit {
			return fc, refuse("an omitted input that is not the default being changed")
		}
		if len(ue) > 0 && !p.typ.hasElem() {
			return fc, refuse("unknown elements of a type with no elements")
		}
		unk := make([]string, 0, len(ue))
		for _, e := range ue {
			k, ok := str(e)
			if !ok || !elementKey.MatchString(k) {
				return fc, refuse("an unknown element is not an index or key")
			}
			unk = append(unk, k)
		}
		n := strconv.Itoa(i)
		fc.vars["ladder_mode_"+n] = mode
		fc.vars["ladder_val_"+n] = nil
		if mode == "known" {
			fc.vars["ladder_val_"+n] = s["value"]
		}
		fc.vars["ladder_sample_"+n] = p.typ.sample()
		if p.typ.hasElem() {
			fc.vars["ladder_unk_"+n] = unk
			fc.vars["ladder_esample_"+n] = p.typ.elem.sample()
		}
		if isOmit {
			fc.vars["ladder_omit"] = mode == "omitted"
		}
		if truthy(s["unknown"]) || len(ue) > 0 {
			fc.unknown = append(fc.unknown, p.name)
		}
	}
	return fc, nil
}

// wrapInput reads the count.index or each.key input of a case: the
// instance the subject is read at.
func wrapInput(fc *fixtureCase, wrap string, v any) error {
	s, ok := v.(map[string]any)
	if ok {
		for _, k := range stateKeys {
			if truthy(s[k]) {
				ok = false
			}
		}
	}
	if !ok {
		return refuse("a count.index or each.key input that is not a known value")
	}
	val, isStr := str(s["value"])
	if wrap == "count" {
		if !isStr || !smallIndex.MatchString(val) {
			return refuse("a count.index input that is not a small index")
		}
		fc.vars["ladder_count_index"] = int(val[0] - '0')
		fc.addr = "terraform_data.subject[" + val + "]"
		return nil
	}
	if !isStr || !eachKey.MatchString(val) {
		return refuse("an each.key input outside the key characters")
	}
	fc.vars["ladder_each_key"] = val
	fc.addr = `terraform_data.subject["` + val + `"]`
	return nil
}

// mainTF builds main.tf, its manifest blocks and the canary names.
func (b *builder) mainTF(wrap string, hasOmit bool) (string, []any, []string, error) {
	vars := map[string]valueType{}
	var standins, locals []string
	var canaries [][2]string
	strType := valueType{kind: "string"}
	for i, p := range b.in {
		n, y := strconv.Itoa(i), p.typ
		isSet := y.kind == "set"
		if isSet {
			vars["ladder_val_"+n] = valueType{kind: "list", elem: y.elem}
		} else {
			vars["ladder_val_"+n] = y
		}
		vars["ladder_mode_"+n] = strType
		vars["ladder_sample_"+n] = y
		s, err := b.fill("standin", map[string]string{"NAME": "ladder_u_" + n, "COUNT": "", "VAR": "ladder_sample_" + n})
		if err != nil {
			return "", nil, nil, err
		}
		standins = append(standins, s)
		canaries = append(canaries, [2]string{"canary_ladder_u_" + n, "terraform_data.ladder_u_" + n + ".output"})
		known := "var.ladder_val_" + n
		if y.hasElem() {
			vars["ladder_unk_"+n] = valueType{kind: "list", elem: &strType}
			vars["ladder_esample_"+n] = *y.elem
			s, err := b.fill("standin", map[string]string{"NAME": "ladder_ue_" + n, "COUNT": "", "VAR": "ladder_esample_" + n})
			if err != nil {
				return "", nil, nil, err
			}
			standins = append(standins, s)
			canaries = append(canaries, [2]string{"canary_ladder_ue_" + n, "terraform_data.ladder_ue_" + n + ".output"})
			pick := "contains(var.ladder_unk_" + n + ", tostring(k)) ? terraform_data.ladder_ue_" + n + ".output : v"
			switch {
			case y.kind == "map":
				known = "tomap({ for k, v in var.ladder_val_" + n + " : k => " + pick + " })"
			case isSet:
				known = "toset([for k, v in var.ladder_val_" + n + " : " + pick + "])"
			default:
				known = "tolist([for k, v in var.ladder_val_" + n + " : " + pick + "])"
			}
		}
		locals = append(locals, "  ladder_in_"+n+" = var.ladder_mode_"+n+` == "unknown" ? terraform_data.ladder_u_`+n+".output : var.ladder_mode_"+n+` == "known" ? `+known+" : null\n")
	}
	if hasOmit {
		vars["ladder_omit"] = valueType{kind: "bool"}
	}
	switch wrap {
	case "count":
		vars["ladder_count_index"] = valueType{kind: "number"}
	case "for_each":
		vars["ladder_each_key"] = strType
	}
	var m strings.Builder
	l3 := b.o.Level == "L3"
	if l3 {
		s, err := b.fill("providers", nil)
		if err != nil {
			return "", nil, nil, err
		}
		m.WriteString(s)
	}
	varNames := slices.Sorted(func(yield func(string) bool) {
		for k := range vars {
			if !yield(k) {
				return
			}
		}
	})
	for _, k := range varNames {
		s, err := b.fill("variable", map[string]string{"NAME": k, "TYPE": vars[k].String()})
		if err != nil {
			return "", nil, nil, err
		}
		m.WriteString(s)
	}
	for _, s := range standins {
		m.WriteString(s)
	}
	if len(locals) > 0 {
		s, err := b.fill("locals", map[string]string{"BODY": strings.Join(locals, "")})
		if err != nil {
			return "", nil, nil, err
		}
		m.WriteString(s)
	}
	for _, c := range canaries {
		s, err := b.fill("canary", map[string]string{"NAME": c[0], "REF": c[1]})
		if err != nil {
			return "", nil, nil, err
		}
		m.WriteString(s)
	}
	s, err := b.fill("known", nil)
	if err != nil {
		return "", nil, nil, err
	}
	m.WriteString(s)

	var blocks []any
	if l3 {
		blocks = append(blocks, blockRef("terraform"), blockRef("provider", strings.SplitN(b.o.Resource, "_", 2)[0]))
	}
	for _, k := range varNames {
		blocks = append(blocks, blockRef("variable", k))
	}
	for i, p := range b.in {
		n := strconv.Itoa(i)
		blocks = append(blocks, blockRef("resource", "terraform_data", "ladder_u_"+n))
		if p.typ.hasElem() {
			blocks = append(blocks, blockRef("resource", "terraform_data", "ladder_ue_"+n))
		}
	}
	if len(locals) > 0 {
		blocks = append(blocks, blockRef("locals"))
	}
	names := make([]string, 0, len(canaries))
	for _, c := range canaries {
		blocks = append(blocks, blockRef("output", c[0]))
		names = append(names, c[0])
	}
	blocks = append(blocks, blockRef("output", "canary_known"))
	return m.String(), blocks, names, nil
}

func blockRef(typ string, labels ...string) map[string]any {
	if labels == nil {
		labels = []string{}
	}
	return map[string]any{"type": typ, "labels": labels}
}

// subjects builds each side's main.tf, subject.tf, test file at L2 and
// manifest.
func (b *builder) subjects(sides []string, exprs map[string]string, wrap, omit string, hasOmit bool, main string, blocks []any) error {
	subj := blockRef("output", "subject")
	sec := "subject-output"
	switch {
	case hasOmit:
		sec = "subject-default"
	case wrap == "count":
		sec = "subject-count"
	case wrap == "for_each":
		sec = "subject-for-each"
	}
	if wrap != "none" {
		subj = blockRef("resource", "terraform_data", "subject")
	}
	if hasOmit && wrap != "none" {
		return refuse("a default change in a counted or for_each context")
	}
	// L3 splices the expression into the target attribute of the resource
	// under test, last in its block; a counted, for_each or default context
	// has no single instance to plan.
	if b.o.Level == "L3" {
		if hasOmit || wrap != "none" {
			return refuse("count, each and default contexts are unverifiable at L3")
		}
		subj, sec = blockRef("resource", b.o.Resource, "this"), "subject"
	}
	// L2 splices it into the count or for_each of terraform_data.subject,
	// whose instances are the observation.
	if b.o.Level == "L2" {
		if hasOmit || wrap != "none" {
			return refuse("count, each and default contexts are unverifiable at L2")
		}
		if b.o.Attribute != "count" && b.o.Attribute != "for_each" {
			return refuse("no L2 template for attribute " + b.o.Attribute)
		}
		subj, sec = blockRef("resource", "terraform_data", "subject"), "subject-l2-"+b.o.Attribute
	}
	local := ""
	for i, p := range b.in {
		if hasOmit && p.name == omit {
			local = "local.ladder_in_" + strconv.Itoa(i)
			break
		}
	}
	rs := make([]rename, 0, len(b.in))
	for i, p := range b.in {
		rs = append(rs, rename{from: p.text, to: "local.ladder_in_" + strconv.Itoa(i), toks: refTokens(p.text)})
	}
	slices.SortStableFunc(rs, func(x, y rename) int {
		if len(x.from) != len(y.from) {
			return len(y.from) - len(x.from)
		}
		return strings.Compare(x.from, y.from)
	})
	attr := "value"
	switch {
	case b.o.Level == "L3" || b.o.Level == "L2":
		attr = b.o.Attribute
	case wrap != "none":
		attr = "input"
	}
	for _, s := range sides {
		vals := map[string]string{"EXPR": renameRefs(exprs[s], rs), "LOCAL": local}
		if b.o.Level == "L3" {
			vals["ATTR"] = b.o.Attribute
		}
		subject, err := b.fill(sec, vals)
		if err != nil {
			return err
		}
		b.files = append(b.files, File{Path: s + "/main.tf", Data: []byte(main)}, File{Path: s + "/subject.tf", Data: []byte(subject)})
		if b.o.Level == "L2" {
			test, err := b.fill("test", nil)
			if err != nil {
				return err
			}
			b.files = append(b.files, File{Path: s + "/ladder.tftest.hcl", Data: []byte(test)})
		}
		manifest, err := encodeCanonical(map[string]any{"schema_version": 1, "files": map[string]any{
			"main.tf": map[string]any{"blocks": blocks},
			"subject.tf": map[string]any{"blocks": []any{subj}, "single_expressions": []any{map[string]any{
				"block": subj["type"], "labels": subj["labels"], "attribute": attr,
			}}},
		}})
		if err != nil {
			return err
		}
		b.files = append(b.files, File{Path: "expect/" + s + ".json", Data: manifest})
	}
	return nil
}

// EncodeResult writes a build result as compact JSON with sorted keys.
func EncodeResult(r map[string]any) ([]byte, error) { return encodeCanonical(r) }
