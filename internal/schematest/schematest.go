// Package schematest validates JSON values against the schemas under
// schema/ in tests. It implements the subset of JSON Schema 2020-12 those
// files use and fails on any keyword outside it, so a schema cannot silently
// use a keyword the tests do not check. Only tests import it.
package schematest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// BaseURL is the prefix of the $id of every schema under schema/.
const BaseURL = "https://github.com/modulestf/facts/schema/"

var schemaKeywords = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true, "$defs": true,
	"type": true, "enum": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "pattern": true, "minLength": true, "minimum": true, "maxItems": true,
	// Used by the model-output schema the merge fixtures are checked against.
	"maxLength": true, "maximum": true,
	"oneOf": true, "allOf": true, "$ref": true,
	// Used by the facts-bundle, finding and schema-handoff schemas the oracle
	// fixtures are checked against.
	"const": true, "not": true, "if": true, "then": true, "minItems": true, "uniqueItems": true,
	"propertyNames": true, "dependentRequired": true, "unevaluatedProperties": true,
}

// jsonCanon renders a decoded JSON value so that two values are equal exactly
// when their texts are: the type is part of the text, so "1" differs from 1,
// numbers are compared by value, so 1 equals 1.0, and object keys are sorted.
func jsonCanon(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("b:%v", x)
	case string:
		return "s:" + strconv.Quote(x)
	case json.Number:
		if r, ok := new(big.Rat).SetString(string(x)); ok {
			return "n:" + r.RatString()
		}
		return "n?:" + string(x)
	case float64:
		if r := new(big.Rat); r.SetFloat64(x) != nil {
			return "n:" + r.RatString()
		}
		return fmt.Sprintf("n?:%v", x)
	case int:
		return "n:" + strconv.Itoa(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = jsonCanon(e)
		}
		return "a:[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := slices.Sorted(maps.Keys(x))
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = strconv.Quote(k) + ":" + jsonCanon(x[k])
		}
		return "o:{" + strings.Join(parts, ",") + "}"
	}
	return fmt.Sprintf("?%T:%v", v, v)
}

// jsonEqual compares two decoded JSON values by type and value, as enum and
// const do.
func jsonEqual(a, b any) bool { return jsonCanon(a) == jsonCanon(b) }

// lookaheadPatterns maps each schema pattern RE2 cannot compile, by its exact
// text, to a hand-written check. A pattern RE2 cannot compile that is missing
// here fails as unsupported, so a changed pattern cannot go unchecked.
var lookaheadPatterns = map[string]func(string) bool{
	// finding.schema.json $defs/path
	`^(\.|(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$)).+)$`: func(x string) bool {
		return x == "." || relPathOK(x)
	},
	// schema-handoff.schema.json $defs/dir_path, interface-diff.schema.json
	`^(\.|(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$))(?!.*/$).+)$`: func(x string) bool {
		return x == "." || relPathOK(x) && !strings.HasSuffix(x, "/")
	},
	// schema-handoff.schema.json inputs file path
	`^(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$)).*\.tf$`: func(x string) bool {
		return relPathOK(x) && strings.HasSuffix(x, ".tf")
	},
}

// relPathOK is the part the path patterns share: non-empty, no leading /, no
// backslash, no .. segment, and none of the line terminators a JavaScript .
// does not match. A . segment stays allowed, as in ajv.
func relPathOK(x string) bool {
	if x == "" || strings.HasPrefix(x, "/") || strings.ContainsAny(x, "\\\n\r\u2028\u2029") {
		return false
	}
	return !slices.Contains(strings.Split(x, "/"), "..")
}

// evaluatedNames returns the property names s and the schemas its $ref
// chain points to declare, the names unevaluatedProperties counts as evaluated.
// shortcut: properties from oneOf, if/then and additionalProperties are not
// counted; extend this if a schema closes one of those with
// unevaluatedProperties.
func (set Set) evaluatedNames(doc string, s map[string]any) map[string]bool {
	names := map[string]bool{}
	props, _ := s["properties"].(map[string]any)
	for k := range props {
		names[k] = true
	}
	if ref, ok := s["$ref"].(string); ok {
		if file, target := set.resolve(doc, ref); target != nil {
			if _, ok := target["properties"].(map[string]any); ok {
				for k := range set.evaluatedNames(file, target) {
					names[k] = true
				}
			}
		}
	}
	return names
}

// Set holds the schemas by $id. A $ref resolves against the $id of
// the schema that holds it, as ajv resolves it.
type Set map[string]map[string]any

// Load reads every *.schema.json file in dir and returns them by $id.
func Load(t testing.TB, dir string) Set {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas: %v", err)
	}
	set := Set{}
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: a schema of this repository
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		id, ok := s["$id"].(string)
		if !ok {
			t.Fatalf("%s: no $id", f)
		}
		set[id] = s
	}
	return set
}

// resolve returns the schema a $ref in the schema with $id doc points to,
// and the $id of the file that holds it.
func (set Set) resolve(doc, ref string) (string, map[string]any) {
	// A relative $id, such as lint.schema.json, resolves under a placeholder
	// base, since url.ResolveReference makes a relative path absolute.
	const relBase = "https://relative.invalid/"
	base, err1 := url.Parse(relBase + doc)
	if strings.Contains(doc, "://") {
		base, err1 = url.Parse(doc)
	}
	r, err2 := url.Parse(ref)
	if err1 != nil || err2 != nil {
		return "", nil
	}
	u := base.ResolveReference(r)
	frag := u.Fragment
	u.Fragment = ""
	id := strings.TrimPrefix(u.String(), relBase)
	var cur any = set[id]
	for tok := range strings.SplitSeq(strings.TrimPrefix(frag, "/"), "/") {
		if tok == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return id, nil
		}
		cur = m[tok]
	}
	target, _ := cur.(map[string]any)
	return id, target
}

// DecodeJSON decodes data with numbers kept as json.Number, as Validate
// expects.
func DecodeJSON(t testing.TB, data []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Validate checks v against the schema whose $id is name, or BaseURL
// followed by name, and returns every violation. A name no loaded schema has
// is a violation: checking against nothing would pass every value.
func (set Set) Validate(name string, v any) []string {
	if set[name] == nil && set[BaseURL+name] != nil {
		name = BaseURL + name
	}
	if set[name] == nil {
		return []string{fmt.Sprintf("no schema with $id %q", name)}
	}
	var errs []string
	set.check(name, set[name], v, "$", &errs)
	return errs
}

func (set Set) check(doc string, s map[string]any, v any, at string, errs *[]string) {
	fail := func(f string, a ...any) { *errs = append(*errs, at+": "+fmt.Sprintf(f, a...)) }
	for k := range s {
		if !schemaKeywords[k] {
			fail("unsupported schema keyword %q", k)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		file, target := set.resolve(doc, ref)
		if target == nil {
			fail("unresolved $ref %q", ref)
			return
		}
		set.check(file, target, v, at, errs)
	}
	if ts, ok := s["type"]; ok {
		var types []string
		switch x := ts.(type) {
		case string:
			types = []string{x}
		case []any:
			for _, y := range x {
				types = append(types, y.(string))
			}
		}
		// JSON Schema counts an integer as a number.
		integerAsNumber := jsonType(v) == "integer" && slices.Contains(types, "number")
		if !slices.Contains(types, jsonType(v)) && !integerAsNumber {
			fail("type %s, want %v", jsonType(v), types)
			return
		}
	}
	if en, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range en {
			if jsonEqual(e, v) {
				found = true
			}
		}
		if !found {
			fail("value %v not in enum %v", v, en)
		}
	}
	if c, ok := s["const"]; ok && !jsonEqual(c, v) {
		fail("value %v is not %v", v, c)
	}
	if n, ok := s["not"].(map[string]any); ok {
		var e []string
		set.check(doc, n, v, at, &e)
		if len(e) == 0 {
			fail("matches a not schema")
		}
	}
	if cond, ok := s["if"].(map[string]any); ok {
		var e []string
		set.check(doc, cond, v, at, &e)
		if then, ok := s["then"].(map[string]any); ok && len(e) == 0 {
			set.check(doc, then, v, at, errs)
		}
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			set.check(doc, sub.(map[string]any), v, at, errs)
		}
	}
	if of, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, sub := range of {
			var e []string
			set.check(doc, sub.(map[string]any), v, at, &e)
			if len(e) == 0 {
				n++
			}
		}
		if n != 1 {
			fail("matches %d oneOf branches", n)
		}
	}
	switch x := v.(type) {
	case string:
		if p, ok := s["pattern"].(string); ok {
			match, known := lookaheadPatterns[p]
			if re, err := regexp.Compile(p); err == nil {
				match, known = re.MatchString, true
			}
			switch {
			case !known:
				fail("unsupported pattern %s: RE2 cannot compile it and no hand-written check is listed", p)
			case !match(x):
				fail("%q does not match %s", x, p)
			}
		}
		// JSON Schema counts characters, not bytes.
		if m, ok := s["minLength"].(float64); ok && utf8.RuneCountInString(x) < int(m) {
			fail("shorter than %v", m)
		}
		if m, ok := s["maxLength"].(float64); ok && utf8.RuneCountInString(x) > int(m) {
			fail("longer than %v", m)
		}
	case json.Number:
		if m, ok := s["minimum"].(float64); ok {
			if f, _ := x.Float64(); f < m {
				fail("below minimum %v", m)
			}
		}
		if m, ok := s["maximum"].(float64); ok {
			if f, _ := x.Float64(); f > m {
				fail("above maximum %v", m)
			}
		}
	case []any:
		if m, ok := s["maxItems"].(float64); ok && len(x) > int(m) {
			fail("more than %v items", m)
		}
		if m, ok := s["minItems"].(float64); ok && len(x) < int(m) {
			fail("fewer than %v items", m)
		}
		if u, ok := s["uniqueItems"].(bool); ok && u {
			seen := map[string]bool{}
			for _, e := range x {
				k := jsonCanon(e)
				if seen[k] {
					fail("duplicate item %s", k)
				}
				seen[k] = true
			}
		}
		if it, ok := s["items"].(map[string]any); ok {
			for i, e := range x {
				set.check(doc, it, e, fmt.Sprintf("%s[%d]", at, i), errs)
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := x[r.(string)]; !ok {
					fail("missing %q", r)
				}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				set.check(doc, ps, x[k], at+"."+k, errs)
			} else if b, ok := props[k].(bool); ok {
				// A boolean schema: true accepts any value, false none.
				if !b {
					fail("property %q is not allowed", k)
				}
			} else if ap, ok := s["additionalProperties"].(bool); ok && !ap {
				fail("unexpected property %q", k)
			}
			if pn, ok := s["propertyNames"].(map[string]any); ok {
				set.check(doc, pn, k, at+"."+k+"(name)", errs)
			}
			dr, _ := s["dependentRequired"].(map[string]any)
			if deps, ok := dr[k].([]any); ok {
				for _, d := range deps {
					if _, ok := x[d.(string)]; !ok {
						fail("%q requires %q", k, d)
					}
				}
			}
		}
		if up, ok := s["unevaluatedProperties"].(bool); ok && !up {
			ev := set.evaluatedNames(doc, s)
			for _, k := range keys {
				if !ev[k] {
					fail("unevaluated property %q", k)
				}
			}
		}
	}
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		// Stricter than JSON Schema on purpose: 1.0 and 1e0 are not integers here, as the host refuses those forms.
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}
