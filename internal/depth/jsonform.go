package depth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
	"github.com/zclconf/go-cty/cty"
)

// maxJSONDepth bounds the nesting of a JSON file.
const maxJSONDepth = 256

// jnode is one JSON value. Objects keep their key order and duplicate keys,
// which the JSON configuration syntax uses for repeated blocks.
type jnode struct {
	kind string // "obj", "arr", "str", "num", "bool", "null"
	s    string
	keys []string
	vals []*jnode
}

// parseJSON reads one JSON document.
func parseJSON(src []byte) (*jnode, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()
	n, err := readJSONNode(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return n, nil
}

func readJSONNode(dec *json.Decoder, depth int) (*jnode, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("nested too deep")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		n := &jnode{kind: "arr"}
		if v == '{' {
			n.kind = "obj"
		}
		for dec.More() {
			if n.kind == "obj" {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				ks, ok := k.(string)
				if !ok {
					return nil, errors.New("a key that is not a string")
				}
				n.keys = append(n.keys, ks)
			}
			c, err := readJSONNode(dec, depth+1)
			if err != nil {
				return nil, err
			}
			n.vals = append(n.vals, c)
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return nil, err
		}
		return n, nil
	case string:
		return &jnode{kind: "str", s: v}, nil
	case json.Number:
		return &jnode{kind: "num", s: v.String()}, nil
	case bool:
		return &jnode{kind: "bool", s: map[bool]string{true: "true", false: "false"}[v]}, nil
	}
	return &jnode{kind: "null"}, nil
}

// canon returns a layout-free text of n that keeps key order.
func (n *jnode) canon() string {
	var b strings.Builder
	n.write(&b)
	return b.String()
}

func (n *jnode) write(b *strings.Builder) {
	switch n.kind {
	case "obj", "arr":
		b.WriteString(n.kind + "(")
		for i, v := range n.vals {
			if n.kind == "obj" {
				q, _ := json.Marshal(n.keys[i])
				b.Write(q)
				b.WriteByte(':')
			}
			v.write(b)
			b.WriteByte(',')
		}
		b.WriteByte(')')
	case "str":
		q, _ := json.Marshal(n.s)
		b.Write(q)
	default:
		b.WriteString(n.s)
	}
}

// pairs returns the key and value pairs of an object, or of every object
// of an array of objects. ok is false for any other value.
func (n *jnode) pairs() (keys []string, vals []*jnode, ok bool) {
	switch n.kind {
	case "obj":
		for i, k := range n.keys {
			if k == "//" {
				continue
			}
			keys, vals = append(keys, k), append(vals, n.vals[i])
		}
		return keys, vals, true
	case "arr":
		for _, v := range n.vals {
			k, vs, ok := v.pairs()
			if !ok || v.kind != "obj" {
				return nil, nil, false
			}
			keys, vals = append(keys, k...), append(vals, vs...)
		}
		return keys, vals, true
	}
	return nil, nil, false
}

// bodies returns the block bodies of a value: one object, or each object of
// an array.
func (n *jnode) bodies() ([]*jnode, bool) {
	switch n.kind {
	case "obj":
		return []*jnode{n}, true
	case "arr":
		for _, v := range n.vals {
			if v.kind != "obj" {
				return nil, false
			}
		}
		return n.vals, true
	}
	return nil, false
}

// exprKeys name the arguments whose JSON strings are bare expressions, not
// templates.
var exprKeys = map[string]bool{
	"depends_on": true, "provider": true, "providers": true, "ignore_changes": true,
	"replace_triggered_by": true, "type": true, "from": true, "to": true,
}

// jsonWalker turns JSON values into args, parsing their strings.
type jsonWalker struct {
	file    string
	lim     facts.Limits
	literal bool // a variables file: strings are values, not templates
	err     string
}

// value adds the expressions and names of v to a. exprMode parses strings
// as bare expressions.
// key is the nearest object key, the attribute name of a string.
func (w *jsonWalker) value(v *jnode, a *arg, exprMode bool, key string) {
	switch v.kind {
	case "obj", "arr":
		for i, c := range v.vals {
			if v.kind == "obj" {
				a.names = append(a.names, v.keys[i])
				w.value(c, a, exprMode || exprKeys[v.keys[i]], v.keys[i])
				continue
			}
			w.value(c, a, exprMode, key)
		}
	case "str":
		if e := w.str(v.s, exprMode); e != nil {
			a.exprs = append(a.exprs, e)
			a.attrs = append(a.attrs, key)
			a.scopes = append(a.scopes, nil) // a JSON dynamic block's iterator is not bound: any string
		}
	}
}

// str parses one JSON string into an expression.
func (w *jsonWalker) str(s string, exprMode bool) hclsyntax.Expression {
	if w.literal {
		return &hclsyntax.LiteralValueExpr{Val: cty.StringVal(s)}
	}
	if !w.bounded(s) {
		w.err = "a string nested too deep"
		return nil
	}
	pos := hcl.Pos{Line: 1, Column: 1}
	var (
		e     hclsyntax.Expression
		diags hcl.Diagnostics
	)
	if exprMode {
		e, diags = hclsyntax.ParseExpression([]byte(s), w.file, pos)
	} else {
		e, diags = hclsyntax.ParseTemplate([]byte(s), w.file, pos)
	}
	if diags.HasErrors() {
		w.err = "a string that does not parse"
		return nil
	}
	return e
}

// bounded checks a string against the bracket nesting and unary operator
// limits before the recursive parser sees it.
func (w *jsonWalker) bounded(s string) bool {
	depth, run := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
			if depth > w.lim.MaxDepth {
				return false
			}
		case ')', ']', '}':
			depth--
		}
		if s[i] == '!' || s[i] == '-' {
			run++
			if run > w.lim.MaxPrefixOps {
				return false
			}
		} else if s[i] != ' ' {
			run = 0
		}
	}
	return len(s) < 1<<20
}

// args returns the args of a JSON block body.
func (w *jsonWalker) args(body *jnode) []*arg {
	keys, vals, ok := body.pairs()
	if !ok {
		w.err = "a block body that is not an object"
		return nil
	}
	by := map[string]*arg{}
	var order []string
	for i, k := range keys {
		v := vals[i]
		names := []string{k}
		if k == "dynamic" {
			ks, vs, ok := v.pairs()
			if !ok {
				w.err = "a dynamic block that is not an object"
				return nil
			}
			for j, label := range ks {
				w.addArg(by, &order, "dynamic."+label, []string{k, label}, vs[j])
			}
			continue
		}
		w.addArg(by, &order, k, names, v)
	}
	out := make([]*arg, 0, len(order))
	for _, k := range order {
		out = append(out, by[k])
	}
	slices.SortFunc(out, func(x, y *arg) int { return strings.Compare(x.name, y.name) })
	return out
}

func (w *jsonWalker) addArg(by map[string]*arg, order *[]string, key string, names []string, v *jnode) {
	a := by[key]
	if a == nil {
		a = &arg{name: key}
		by[key] = a
		*order = append(*order, key)
	}
	a.canon += v.canon() + "\x01"
	a.names = append(a.names, names...)
	w.value(v, a, exprKeys[names[0]], names[len(names)-1])
}

// jsonConfig adds the items of one .tf.json file.
func jsonConfig(mod *module, file string, src []byte, lim facts.Limits) string {
	root, err := parseJSON(src)
	if err != nil {
		return "does not parse"
	}
	w := &jsonWalker{file: file, lim: lim}
	keys, vals, ok := root.pairs()
	if !ok || root.kind != "obj" {
		return "not a JSON object"
	}
	add := func(kind, addr, typ string, labels []string, body *jnode) {
		bodies, ok := body.bodies()
		if !ok {
			w.err = "a block that is not an object"
			return
		}
		for _, b := range bodies {
			it := &item{kind: kind, labels: labels, addr: addr, typ: typ, file: file, dir: mod.dir, line: 1,
				args: w.args(b), canon: b.canon()}
			if addr == "" || kind == kProvider || kind == kTerraform {
				it.addr = kind + "#" + strings.Join(labels, ".") + "#" + it.canon
			}
			mod.items = append(mod.items, it)
			if kind == kTerraform {
				mod.requiredJSON(b)
			}
		}
	}
	for i, k := range keys {
		v := vals[i]
		switch k {
		case "resource", "data":
			tks, tvs, ok := v.pairs()
			if !ok {
				return "a " + k + " value that is not an object"
			}
			for j, typ := range tks {
				nks, nvs, ok := tvs[j].pairs()
				if !ok {
					return "a " + k + " value that is not an object"
				}
				for x, name := range nks {
					if k == "resource" {
						add(kResource, typ+"."+name, typ, []string{typ, name}, nvs[x])
					} else {
						add(kData, "data."+typ+"."+name, typ, []string{typ, name}, nvs[x])
					}
				}
			}
		case "module", "variable", "output", "provider":
			nks, nvs, ok := v.pairs()
			if !ok {
				return "a " + k + " value that is not an object"
			}
			prefix := map[string]string{"module": "module.", "variable": "var.", "output": "output.", "provider": ""}[k]
			kind := map[string]string{"module": kModule, "variable": kVariable, "output": kOutput, "provider": kProvider}[k]
			for x, name := range nks {
				add(kind, prefix+name, "", []string{name}, nvs[x])
			}
		case "locals":
			nks, nvs, ok := v.pairs()
			if !ok {
				return "a locals value that is not an object"
			}
			for x, name := range nks {
				a := &arg{name: name, canon: nvs[x].canon()}
				w.value(nvs[x], a, false, name)
				mod.items = append(mod.items, &item{kind: kLocal, addr: "local." + name, file: file, dir: mod.dir, line: 1,
					args: []*arg{a}, canon: a.canon})
			}
		case "terraform":
			add(kTerraform, "", "", nil, v)
		case "moved", "import", "removed", "check":
			add(k, "", "", nil, v)
		default:
			add(kOther, "", "", []string{k}, v)
		}
		if w.err != "" {
			return w.err
		}
	}
	return w.err
}

// requiredJSON records the required_providers entries of a JSON terraform
// block.
func (mod *module) requiredJSON(body *jnode) {
	keys, vals, _ := body.pairs()
	for i, k := range keys {
		if k != "required_providers" {
			continue
		}
		lks, lvs, ok := vals[i].pairs()
		if !ok {
			mod.reqBad = true
			continue
		}
		for j, local := range lks {
			rp := reqProvider{local: local}
			v := lvs[j]
			switch v.kind {
			case "str":
				rp.version, rp.hasVersion = v.s, true
				mod.reqBad = mod.reqBad || strings.Contains(v.s, "${") || strings.Contains(v.s, "%{")
			case "obj":
				for x, key := range v.keys {
					val := v.vals[x]
					lit := val.kind == "str" && !strings.Contains(val.s, "${") && !strings.Contains(val.s, "%{")
					switch key {
					case "source":
						rp.source, rp.hasSource = val.s, true
						mod.reqBad = mod.reqBad || !lit
					case "version":
						rp.version, rp.hasVersion = val.s, true
						mod.reqBad = mod.reqBad || !lit
					}
				}
			default:
				mod.reqBad = true
			}
			mod.req = append(mod.req, rp)
		}
	}
}

// jsonFile reads a Terraform-read JSON file that is not configuration.
func jsonFile(fm *fileModel, src []byte, lim facts.Limits) string {
	root, err := parseJSON(src)
	if err != nil {
		return "does not parse"
	}
	w := &jsonWalker{file: fm.path, lim: lim, literal: fm.form == formVars}
	keys, vals, ok := root.pairs()
	if !ok || root.kind != "obj" {
		return "not a JSON object"
	}
	dir := path.Dir(fm.path)
	for i, k := range keys {
		kind, addr := kFileAttr, k
		if fm.form == formVars {
			kind, addr = kTFVar, "var."+k
		}
		a := &arg{name: k, canon: vals[i].canon(), names: []string{k}}
		w.value(vals[i], a, false, k)
		fm.items = append(fm.items, &item{kind: kind, addr: addr, file: fm.path, dir: dir, line: 1, args: []*arg{a}, canon: a.canon})
	}
	return w.err
}
