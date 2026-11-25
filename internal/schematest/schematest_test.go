package schematest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSchemaValidatorKeywords checks the keywords added for the oracle
// schemas on a schema of their own.
func TestSchemaValidatorKeywords(t *testing.T) {
	set := Set{
		"base.json": {"properties": map[string]any{"a": map[string]any{}}},
		"t.json": {
			"$ref":                  "base.json",
			"properties":            map[string]any{"b": map[string]any{"const": "x"}, "c": map[string]any{}, "l": map[string]any{"minItems": 1.0, "uniqueItems": true}, "e": true, "f": false, "s": map[string]any{"const": "1"}, "m": map[string]any{"enum": []any{1.0}}, "x": map[string]any{"maxLength": 2.0}, "y": map[string]any{"minLength": 2.0}, "n": map[string]any{"maximum": 3.0}},
			"dependentRequired":     map[string]any{"b": []any{"c"}},
			"propertyNames":         map[string]any{"pattern": "^[a-z]$"},
			"unevaluatedProperties": false,
			"if":                    map[string]any{"required": []any{"a"}},
			"then":                  map[string]any{"properties": map[string]any{"c": map[string]any{"not": map[string]any{"const": "no"}}}},
		},
	}
	if errs := set.Validate("t.json", DecodeJSON(t, []byte(`{"a":1,"b":"x","c":"yes","e":null,"l":[1,2],"s":"1","m":1.0,"x":"\u00e9\u00e9","y":"\u00e9\u00e9","n":3}`))); len(errs) > 0 {
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
		"maxLength":             `{"x":"abc"}`,
		"minLength characters":  `{"y":"\u00e9"}`,
		"maximum":               `{"n":4}`,
	} {
		if errs := set.Validate("t.json", DecodeJSON(t, []byte(doc))); len(errs) == 0 {
			t.Errorf("%s: accepted %s", name, doc)
		}
	}
	// A value the if schema does not match skips then.
	if errs := set.Validate("t.json", DecodeJSON(t, []byte(`{"c":"no"}`))); len(errs) > 0 {
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
		walk(DecodeJSON(t, data))
	}
	for p := range lookaheadPatterns {
		if !used[p] {
			t.Errorf("hand-written check for %s, which no schema uses", p)
		}
	}
	// A pattern RE2 cannot compile and the table lacks fails as unsupported.
	set := Set{"p.json": {"type": "string", "pattern": `^(?!x).*$`}}
	if errs := set.Validate("p.json", "y"); len(errs) != 1 || !strings.Contains(errs[0], "unsupported pattern") {
		t.Errorf("unknown lookahead pattern: %v", errs)
	}
}
