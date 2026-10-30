package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mutateOracle copies every fixture, so the positive case check holds on the
// copy, and applies mutate, when set, to the parsed fixture/file.
func mutateOracle(t *testing.T, fixture, file string, mutate func(e map[string]any)) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "oracle")
	if err := os.CopyFS(dst, os.DirFS(oracleRoot)); err != nil {
		t.Fatal(err)
	}
	if mutate == nil {
		return dst
	}
	p := filepath.Join(dst, fixture, file)
	data, err := os.ReadFile(p) //nolint:gosec // G304: the temp copy
	if err != nil {
		t.Fatal(err)
	}
	e, ok := decodeJSON(t, data).(map[string]any)
	if !ok {
		t.Fatalf("%s/%s is not an object", fixture, file)
	}
	mutate(e)
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// Helpers over a parsed expected.json, for the mutations.
func manifestOf(e map[string]any) []any { return e["manifest"].([]any) }

func entryOf(t *testing.T, e map[string]any, rule string) map[string]any {
	t.Helper()
	for _, m := range manifestOf(e) {
		if m := m.(map[string]any); m["rule_id"] == rule {
			return m
		}
	}
	t.Fatalf("no manifest entry for %s", rule)
	return nil
}

func itemsOf(e map[string]any, key string) []any { return e[key].([]any) }

func obj(v any) map[string]any { return v.(map[string]any) }

// TestOracleChecksCatchMutations mutates one oracle file at a time and checks
// that the check meant to catch it fails, with the message it is meant to give.
func TestOracleChecksCatchMutations(t *testing.T) {
	reg := pinnedOracleRegistry(t)
	set := oracleSchemas(t)
	// A baseline that does not pass would make every mutation look caught.
	if bad := append(checkOracleSchemas(t, set, oracleRoot), checkOracleInvariants(reg, oracleRoot)...); len(bad) > 0 {
		t.Fatalf("the committed fixtures do not pass, so no mutation can be judged:\n%s", strings.Join(bad, "\n"))
	}
	rootKind := func(rule string) bool {
		r, _ := reg.Rule(rule)
		return slices.Contains(r.AreaKinds, "root")
	}
	const sp, x = "structure-positive", "expected.json"
	for _, c := range []struct {
		name, fixture, file string
		schema              bool // the schema check, not the invariants, catches it
		want                string
		mutate              func(t *testing.T, e map[string]any)
	}{
		{"missing entry", sp, x, false, "missing entry", func(_ *testing.T, e map[string]any) {
			e["manifest"] = manifestOf(e)[1:]
		}},
		{"duplicate entry", sp, x, false, "duplicate entry", func(_ *testing.T, e map[string]any) {
			e["manifest"] = append(manifestOf(e), manifestOf(e)[1])
		}},
		{"applicable rule marked not_applicable", sp, x, false, "outside area_kinds", func(t *testing.T, e map[string]any) {
			entryOf(t, e, "structure.variable-untyped")["status"] = "not_applicable"
		}},
		{"candidate status with no candidate", sp, x, false, "is candidate with no candidate there", func(_ *testing.T, e map[string]any) {
			var kept []any
			for _, c := range itemsOf(e, "candidates") {
				if obj(c)["rule_id"] != "structure.wrong-file" {
					obj(c)["candidate_id"] = fmt.Sprintf("C-%03d", len(kept)+1)
					kept = append(kept, c)
				}
			}
			e["candidates"] = kept
		}},
		{"status outside the schema enum", sp, x, true, "$.manifest[0]: matches 0 oneOf branches", func(_ *testing.T, e map[string]any) {
			obj(manifestOf(e)[0])["status"] = "skipped"
		}},
		{"reason code outside the closed list", sp, x, false, "not in the closed list", func(t *testing.T, e map[string]any) {
			entryOf(t, e, "coverage.unknown-argument")["reason_code"] = "schema_missing"
		}},
		{"not_evaluated entry with no reason code", sp, x, false, "reason_code must be present exactly on not_evaluated", func(t *testing.T, e map[string]any) {
			delete(entryOf(t, e, "coverage.unknown-argument"), "reason_code")
		}},
		{"reason code on a clean entry", sp, x, false, "reason_code must be present exactly on not_evaluated", func(t *testing.T, e map[string]any) {
			entryOf(t, e, "structure.missing-description")["status"] = "clean"
			entryOf(t, e, "structure.missing-description")["reason_code"] = "parse_error"
		}},
		{"rule outside the registry tiers", sp, x, false, "not a tool or candidate rule", func(_ *testing.T, e map[string]any) {
			obj(manifestOf(e)[0])["rule_id"] = "review.not-a-rule"
		}},
		{"area that is not an area", sp, x, false, "is not ., modules/<name> or examples/<name>", func(_ *testing.T, e map[string]any) {
			e["manifest"] = append(manifestOf(e), map[string]any{"rule_id": obj(manifestOf(e)[0])["rule_id"], "area": "docs", "status": "clean"})
		}},
		{"severity outside the registry", sp, x, false, "severity LOW not in", func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "findings")[0])["severity"] = "LOW"
		}},
		{"finding of a candidate rule", sp, x, false, "is not a tool rule", func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "findings")[0])["rule_id"] = obj(itemsOf(e, "candidates")[0])["rule_id"]
		}},
		{"ids out of sequence", sp, x, false, "out of sequence", func(_ *testing.T, e map[string]any) {
			c := itemsOf(e, "candidates")
			obj(c[0])["candidate_id"], obj(c[1])["candidate_id"] = obj(c[1])["candidate_id"], obj(c[0])["candidate_id"]
		}},
		{"line outside the subject block", sp, x, false, "outside the block of", func(_ *testing.T, e map[string]any) {
			for _, c := range itemsOf(e, "candidates") {
				if obj(c)["rule_id"] == "structure.argument-order" {
					obj(c)["line"] = 1
				}
			}
		}},
		{"evidence text not in the tree", sp, x, false, "does not match the tree", func(_ *testing.T, e map[string]any) {
			sl := obj(obj(obj(itemsOf(e, "candidates")[0])["evidence"])["source_lines"].([]any)[0])
			sl["text"] = sl["text"].(string) + " "
		}},
		{"subject not scoped to its area", sp, x, false, "neither the file path nor scoped to area", func(_ *testing.T, e map[string]any) {
			c := obj(itemsOf(e, "candidates")[0])
			_, addr, _ := strings.Cut(c["subject"].(string), ":")
			c["subject"] = addr
		}},
		// A root entry for every non-cross-area rule, each consistent with
		// area_kinds, so that only the scope check can catch it.
		{"root in scope when the change does not touch it", "submodule-only", x, false, "but the change touches", func(_ *testing.T, e map[string]any) {
			for _, m := range manifestOf(e) {
				if m := obj(m); m["area"] == "modules/extra" {
					status := "not_applicable"
					if rootKind(m["rule_id"].(string)) {
						status = "clean"
					}
					e["manifest"] = append(manifestOf(e), map[string]any{"rule_id": m["rule_id"], "area": ".", "status": status})
				}
			}
		}},
		{"touched area left out of scope", "example-only", x, false, "but the change touches", func(_ *testing.T, e map[string]any) {
			var kept []any
			for _, m := range manifestOf(e) {
				if obj(m)["area"] != "examples/basic" {
					kept = append(kept, m)
				}
			}
			e["manifest"] = kept
		}},
		{"handoff hash not of the tree", "coverage-positive", "schema-handoff.json", false, "inputs.head does not match head/", func(_ *testing.T, e map[string]any) {
			f := obj(obj(obj(e["inputs"])["head"])["files"].([]any)[0])
			f["sha256"] = strings.Repeat("0", 64)
		}},
		{"handoff outside its schema", "coverage-positive", "schema-handoff.json", true, "unexpected property", func(_ *testing.T, e map[string]any) {
			e["extra"] = true
		}},
		// The path patterns RE2 cannot compile, one field each.
		{"finding file with a leading slash", sp, x, true, `$.findings[0].file: "/a.tf" does not match`, func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "findings")[0])["file"] = "/a.tf"
		}},
		{"candidate file with a backslash", sp, x, true, `$.candidates[0].file: "a\\b" does not match`, func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "candidates")[0])["file"] = `a\b`
		}},
		{"evidence file with a .. segment", sp, x, true, `$.candidates[0].evidence.source_lines[0].file: "../up" does not match`, func(_ *testing.T, e map[string]any) {
			obj(obj(obj(itemsOf(e, "candidates")[0])["evidence"])["source_lines"].([]any)[0])["file"] = "../up"
		}},
		{"handoff directory with a trailing slash", "coverage-positive", "schema-handoff.json", true, `$.directories[0].path: "dir/" does not match`, func(_ *testing.T, e map[string]any) {
			obj(e["directories"].([]any)[0])["path"] = "dir/"
		}},
		{"handoff input that is not a .tf file", "coverage-positive", "schema-handoff.json", true, `$.inputs.head.files[0].path: "main.tfx" does not match`, func(_ *testing.T, e map[string]any) {
			obj(obj(obj(e["inputs"])["head"])["files"].([]any)[0])["path"] = "main.tfx"
		}},
		{"cross-area entry outside . with no item", "example-only", x, false, "a cross-area rule has an entry outside . only where its items are", func(t *testing.T, e map[string]any) {
			for _, m := range manifestOf(e) {
				if r, _ := reg.Rule(obj(m)["rule_id"].(string)); r.CrossArea {
					obj(m)["area"] = "examples/basic"
					return
				}
			}
			t.Fatal("example-only has no cross-area entry")
		}},
		{"line not in the head file", sp, x, false, "line 99999 is not in head/main.tf", func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "candidates")[1])["line"] = 99999
		}},
		{"subject names no block", sp, x, false, "outside the block of .:aws_demo_widget.nonexistent", func(_ *testing.T, e map[string]any) {
			obj(itemsOf(e, "candidates")[1])["subject"] = ".:aws_demo_widget.nonexistent"
		}},
		// The only positive case of the rule read as clean, consistent in its
		// own fixture.
		{"rule with no positive case", "compat-versions-floor", x, false, "compat.provider-floor-raised: no fixture has a positive case", func(t *testing.T, e map[string]any) {
			e["candidates"] = []any{}
			entryOf(t, e, "compat.provider-floor-raised")["status"] = "clean"
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := mutateOracle(t, c.fixture, c.file, func(e map[string]any) { c.mutate(t, e) })
			var bad []string
			if c.schema {
				bad = checkOracleSchemas(t, set, dir)
			} else {
				bad = checkOracleInvariants(reg, dir)
			}
			if !slices.ContainsFunc(bad, func(b string) bool { return strings.Contains(b, c.want) }) {
				t.Errorf("not caught for the intended reason %q; got:\n%s", c.want, strings.Join(bad, "\n"))
			}
		})
	}
}

// TestOracleChecksCatchMissingHead removes a fixture's head/.
func TestOracleChecksCatchMissingHead(t *testing.T) {
	reg := pinnedOracleRegistry(t)
	dir := mutateOracle(t, "structure-positive", "expected.json", nil)
	if err := os.RemoveAll(filepath.Join(dir, "structure-positive", "head")); err != nil {
		t.Fatal(err)
	}
	if bad := checkOracleInvariants(reg, dir); !slices.Contains(bad, "structure-positive: no head/") {
		t.Errorf("not caught for the intended reason; got:\n%s", strings.Join(bad, "\n"))
	}
}
