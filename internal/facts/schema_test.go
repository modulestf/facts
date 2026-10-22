package facts

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
)

// This file holds a validator for the subset of JSON Schema 2020-12 the files
// in schema/ use. It fails on any keyword outside that subset, so a schema
// cannot silently use a keyword the tests do not check.

var schemaKeywords = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true, "$defs": true,
	"type": true, "enum": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "pattern": true, "minLength": true, "minimum": true, "maxItems": true,
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
func (set schemaSet) evaluatedNames(doc string, s map[string]any) map[string]bool {
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

// schemaSet holds the schemas by $id. A $ref resolves against the $id of
// the schema that holds it, as ajv resolves it.
type schemaSet map[string]map[string]any

func loadSchemas(t *testing.T) schemaSet {
	t.Helper()
	files, err := filepath.Glob("../../schema/*.schema.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no schemas: %v", err)
	}
	set := schemaSet{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		set[s["$id"].(string)] = s
	}
	return set
}

// resolve returns the schema a $ref in the schema with $id doc points to,
// and the $id of the file that holds it.
func (set schemaSet) resolve(doc, ref string) (string, map[string]any) {
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

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// validate checks v against schema file name and returns every violation.
func (set schemaSet) validate(name string, v any) []string {
	var errs []string
	set.check(name, set[name], v, "$", &errs)
	return errs
}

func (set schemaSet) check(doc string, s map[string]any, v any, at string, errs *[]string) {
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
		if m, ok := s["minLength"].(float64); ok && len(x) < int(m) {
			fail("shorter than %v", m)
		}
	case json.Number:
		if m, ok := s["minimum"].(float64); ok {
			if f, _ := x.Float64(); f < m {
				fail("below minimum %v", m)
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

func TestSchemaValidatorRejects(t *testing.T) {
	set := loadSchemas(t)
	bad := map[string]string{
		"missing field": `[{"rule_id":"structure.variable-untyped","area":"."}]`,
		"bad status":    `[{"rule_id":"structure.variable-untyped","area":".","status":"ok"}]`,
		"extra field":   `[{"rule_id":"structure.variable-untyped","area":".","status":"clean","x":1}]`,
		"bad rule id":   `[{"rule_id":"Structure","area":".","status":"clean"}]`,
		"empty reason":  `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"","reason_code":"parse_error"}]`,
		"no code":       `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"x"}]`,
		"open code":     `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"x","reason_code":"other"}]`,
		"code on clean": `[{"rule_id":"a.b","area":".","status":"clean","reason_code":"parse_error"}]`,
		"not an array":  `{}`,
	}
	for name, doc := range bad {
		if errs := set.validate("manifest.schema.json", decodeJSON(t, []byte(doc))); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	good := `[{"rule_id":"a.b-c","area":"modules/x","status":"not_evaluated","reason":"main.tf: parse error","reason_code":"parse_error"}]`
	if errs := set.validate("manifest.schema.json", decodeJSON(t, []byte(good))); len(errs) > 0 {
		t.Error(errs)
	}
}

func TestReportMatchesSchemas(t *testing.T) {
	set := loadSchemas(t)
	base := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": ""})
	head := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": "# Free-form.\nvariable \"x\" { type = any }\n", "modules/b/main.tf": "variable {", "examples/e/x.tf": "->../../main.tf"})
	reg := filepath.Join(t.TempDir(), "rule-facts.md")
	if err := os.WriteFile(reg, []byte("# registry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{
		{Head: head},
		{Head: head, Base: base, HeadSHA: strings.Repeat("a", 40), MergeBaseSHA: strings.Repeat("b", 64), RegistryFile: reg},
	} {
		o.Version, o.BinarySHA256, o.Limits = "test", strings.Repeat("0", 64), DefaultLimits
		r, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		checkReport(t, set, r)
		// The keys later inputs and builders fill, filled by hand. The second
		// run has a base and both commits, so InterfaceDiff is filled already.
		sum := strings.Repeat("c", 64)
		r.Envelope.ProfileSHA256 = &sum
		r.Envelope.SchemaInputs = &SchemaInputs{HandoffSHA256: sum, HeadSHA: strings.Repeat("a", 40), MergeBaseSHA: strings.Repeat("b", 40),
			TreeHashes: TreeHashes{Head: sum, MergeBase: sum}, SchemaFiles: []FileHash{{Path: "aws.json", SHA256: sum}}}
		r.Index = []IndexEntry{{File: "modules/a/main.tf", StartLine: 2, EndLine: 2, Subject: "modules/a:var.x"}}
		checkReport(t, set, r)
	}
}

// checkReport validates a report against the lint, envelope and manifest
// schemas, and requires one not_evaluated entry with a reason code.
func checkReport(t *testing.T, set schemaSet, r *Report) {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	v := decodeJSON(t, data)
	if errs := set.validate("lint.schema.json", v); len(errs) > 0 {
		t.Errorf("lint schema: %v\n%s", errs, data)
	}
	m := v.(map[string]any)
	if errs := set.validate("envelope.schema.json", m["envelope"]); len(errs) > 0 {
		t.Errorf("envelope schema: %v", errs)
	}
	if errs := set.validate("manifest.schema.json", m["manifest"]); len(errs) > 0 {
		t.Errorf("manifest schema: %v", errs)
	}
	if !strings.Contains(string(data), `"reason_code":"parse_error"`) {
		t.Errorf("no not_evaluated entry with a reason code: %s", data)
	}
}

func TestRunRejectsBadInputs(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": ""})
	for name, o := range map[string]Options{
		"short sha":    {Head: head, HeadSHA: "abc"},
		"upper sha":    {Head: head, MergeBaseSHA: strings.Repeat("A", 40)},
		"no registry":  {Head: head, RegistryFile: filepath.Join(head, "missing.md")},
		"dir registry": {Head: head, RegistryFile: head},
		"no head":      {Head: filepath.Join(head, "missing")},
	} {
		o.Limits = DefaultLimits
		if _, err := Run(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRegistryHash(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": "", "rule-facts.md": "abc"})
	r, err := Run(Options{Head: head, RegistryFile: filepath.Join(head, "rule-facts.md"), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // sha256("abc")
	if r.Envelope.RegistrySHA256 == nil || *r.Envelope.RegistrySHA256 != want {
		t.Errorf("registry hash %v", r.Envelope.RegistrySHA256)
	}
}

// TestSchemaValidatorKeywords checks the keywords added for the oracle
// schemas on a schema of their own.
func TestSchemaValidatorKeywords(t *testing.T) {
	set := schemaSet{
		"base.json": {"properties": map[string]any{"a": map[string]any{}}},
		"t.json": {
			"$ref":                  "base.json",
			"properties":            map[string]any{"b": map[string]any{"const": "x"}, "c": map[string]any{}, "l": map[string]any{"minItems": 1.0, "uniqueItems": true}, "e": true, "f": false, "s": map[string]any{"const": "1"}, "m": map[string]any{"enum": []any{1.0}}},
			"dependentRequired":     map[string]any{"b": []any{"c"}},
			"propertyNames":         map[string]any{"pattern": "^[a-z]$"},
			"unevaluatedProperties": false,
			"if":                    map[string]any{"required": []any{"a"}},
			"then":                  map[string]any{"properties": map[string]any{"c": map[string]any{"not": map[string]any{"const": "no"}}}},
		},
	}
	if errs := set.validate("t.json", decodeJSON(t, []byte(`{"a":1,"b":"x","c":"yes","e":null,"l":[1,2],"s":"1","m":1.0}`))); len(errs) > 0 {
		t.Errorf("good value rejected: %v", errs)
	}
	for name, doc := range map[string]string{
		"const":                 `{"b":"y","c":1}`,
		"dependentRequired":     `{"b":"x"}`,
		"unevaluatedProperties": `{"d":1}`,
		"propertyNames":         `{"A":1}`,
		"minItems":              `{"l":[]}`,
		"uniqueItems":           `{"l":[{"k":1},{"k":1}]}`,
		"if then not":           `{"a":1,"c":"no"}`,
		"false schema":          `{"f":1}`,
		"const type":            `{"s":1}`,
		"enum type":             `{"m":"1"}`,
		"uniqueItems numbers":   `{"l":[1,1.0]}`,
	} {
		if errs := set.validate("t.json", decodeJSON(t, []byte(doc))); len(errs) == 0 {
			t.Errorf("%s: accepted %s", name, doc)
		}
	}
	// A value the if schema does not match skips then.
	if errs := set.validate("t.json", decodeJSON(t, []byte(`{"c":"no"}`))); len(errs) > 0 {
		t.Errorf("then applied without if: %v", errs)
	}
}

// TestLookaheadPatterns checks each hand-written path check against the
// results of its exact pattern text under a JavaScript RegExp, the engine
// ajv uses, recorded case by case: 1 for a match, 0 for none.
func TestLookaheadPatterns(t *testing.T) {
	cases := []string{".", "", "a.tf", "/a.tf", "../up", "a/../b", "a/..", "..", "...", "a/.../b", "a\\b", "dir/", "./a.tf", "a/./b.tf", "a//b.tf", ".tf", "a.tf\n", "a\nb.tf", "a\rb.tf", "a\u2028b.tf", "a\u2029.tf", "modules/x", "x.tfx", "..a/b.tf", "a..b.tf", "/", "./", "a/..b"}
	want := map[string]string{
		`^(\.|(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$)).+)$`:         "1010000011011111000001111011",
		`^(\.|(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$))(?!.*/$).+)$`: "1010000011001111000001111001",
		`^(?!/)(?!.*\\)(?!(.*/)?\.\.(/|$)).*\.tf$`:          "0010000000001111000000011000",
	}
	if len(want) != len(lookaheadPatterns) {
		t.Fatalf("%d recorded patterns, %d hand-written checks", len(want), len(lookaheadPatterns))
	}
	for p, w := range want {
		match, ok := lookaheadPatterns[p]
		if !ok {
			t.Errorf("no hand-written check for %s", p)
			continue
		}
		for i, c := range cases {
			if got := match(c); got != (w[i] == '1') {
				t.Errorf("%s on %q: got %v, want %v", p, c, got, !got)
			}
		}
	}
	// Every pattern RE2 cannot compile in the schemas has a check, and every
	// check is for a pattern a schema has.
	used := map[string]bool{}
	files, _ := filepath.Glob("../../schema/*.schema.json")
	files = append(files, "../../testdata/oracle/expected.schema.json")
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: a schema of this repository
		if err != nil {
			t.Fatal(err)
		}
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, e := range x {
					if p, ok := e.(string); ok && k == "pattern" {
						if _, err := regexp.Compile(p); err != nil {
							used[p] = true
							if lookaheadPatterns[p] == nil {
								t.Errorf("%s: pattern %s has no hand-written check", f, p)
							}
						}
					} else {
						walk(e)
					}
				}
			case []any:
				for _, e := range x {
					walk(e)
				}
			}
		}
		walk(decodeJSON(t, data))
	}
	for p := range lookaheadPatterns {
		if !used[p] {
			t.Errorf("hand-written check for %s, which no schema uses", p)
		}
	}
	// A pattern RE2 cannot compile and the table lacks fails as unsupported.
	set := schemaSet{"p.json": {"type": "string", "pattern": `^(?!x).*$`}}
	if errs := set.validate("p.json", "y"); len(errs) != 1 || !strings.Contains(errs[0], "unsupported pattern") {
		t.Errorf("unknown lookahead pattern: %v", errs)
	}
}
