package depth

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// gate lists the release-gate fixtures of DECISIONS.md D-1, the fixtures
// of the lead's rulings, the schema tokens and the benchmark shapes, by kind. Each is testdata/<name>.txt; a missing file or
// a fixture of another kind fails the test.
var gate = map[string][]string{
	"must-be-deep": {
		"deep-data-arg-for-each", "deep-data-arg-for-each-via-output", "deep-cli-config", "deep-wrappers-nested",
		"deep-provider-alias", "deep-resource-provider-meta", "deep-data-provider-meta", "deep-depends-on",
		"deep-terraform-backend", "deep-terraform-cloud", "deep-terraform-required-providers-source",
		"deep-terraform-required-version", "deep-lock-file", "deep-policy-arn", "deep-policy-data",
		"deep-policy-document-statement", "deep-policy-dynamic", "deep-policy-local", "deep-policy-child-output",
		"deep-count", "deep-local-feeds-count", "deep-child-output-feeds-count", "deep-module-arg-feeds-for-each",
		"deep-removed-output", "deep-ci-workflow-mixed", "deep-action-yml", "deep-docs-policy-json",
		"deep-changed-symlink", "deep-case-collision", "deep-hostile-filename", "deep-unparseable",
	},
	"must-not-be-light": {
		"notlight-provider-config-path", "notlight-chart-dir", "notlight-unchanged-symlink", "notlight-file-literal",
		"notlight-template-dir", "notlight-fileset-variable", "notlight-file-local", "notlight-tfvars-json",
		"notlight-provider-function",
	},
	"must-be-light": {"light-root-readme"},
	"other": {
		"class-check-consumer", "class-classifier-missing", "class-files-list-mismatch", "class-json-count",
		"class-lifecycle", "class-module-version", "class-moved-added", "class-output-sensitive",
		"class-provisioner", "class-registry-module-consumer", "class-test-file", "class-too-many-examples",
		"class-too-many-files", "review-h1-merge-fanout", "review-h2-local-chain", "review-h3-coalesce-fanout",
		"review-l1-remote-source-path", "review-l2-json-list-source", "review-p1-policy-types",
		"review-s1-link-dotdot", "review-s2-computed-key", "review-s3-case-fold", "review-t1-identity-arg",
		"review-t5-tfvars-no-module", "review-t5b-auto-tfvars", "rule-attr-other-into-count",
		"rule-attr-same-into-count", "rule-depends-on-reference", "rule-log-group-name-not-a-sink",
		"rule-name-interpolation-light", "rule-sibling-module-output-other-attr", "rule-sink-variable-path",
		"rule-whole-object-into-module-for-each", "rule-wrapper-passthrough-output", "rule3-command-hit",
		"rule3-command-no-hit", "rule3-file-provisioner-source", "rule3-file-type-attribute-traced",
		"rule3-iam-path-default", "rule3-local-module-arg-traced", "rule3-remote-module-sink-arg",
		"rule3-sink-var-default", "rule3-tfvars-traced", "rule4-example-built-path", "rule4-field-sibling-built",
		"rule4-nested-dynamic-built", "rule4-nested-dynamic-iterator", "rule4-sink-var-external",
		"rule4-try-each-value", "rule5-lookup-index-nested", "rule5-suffix-sink-built",
		"rule5-suffix-sink-external", "schema-tokens",
	},
	"bench": {
		"bench-1-readme-only", "bench-2-resource-expression", "bench-3-data-argument-added",
		"bench-4-module-argument", "bench-5-twenty-files",
	},
}

func TestFixtures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".txt"))
	}
	for kind, list := range gate {
		for _, n := range list {
			if !slices.Contains(names, n) {
				t.Errorf("gate fixture %s (%s) is missing", n, kind)
			}
		}
	}
	for _, f := range files {
		fx := loadFixture(t, f)
		t.Run(fx.name, func(t *testing.T) {
			for kind, list := range gate {
				if slices.Contains(list, fx.name) && fx.one("kind") != kind {
					t.Fatalf("kind %q, the gate lists it as %s", fx.one("kind"), kind)
				}
			}
			fx.check(t, fx.run(t))
		})
	}
}

// TestReasonsDocumented checks that README.md lists the reason tokens in
// their canonical order, the host tokens after them.
func TestReasonsDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	start := strings.Index(s, "<!-- depth-reasons -->")
	end := strings.Index(s, "<!-- /depth-reasons -->")
	if start < 0 || end < start {
		t.Fatal("README.md has no depth-reasons block")
	}
	got := regexp.MustCompile("`((?:light|focused|deep)-[a-z-]+)`").FindAllStringSubmatch(s[start:end], -1)
	var tokens []string
	for _, m := range got {
		tokens = append(tokens, m[1])
	}
	want := append(slices.Clone(Reasons), HostReasons...)
	if !slices.Equal(tokens, want) {
		t.Errorf("README tokens\n%v\nwant\n%v", tokens, want)
	}
}

func TestBuiltinFunctionsSorted(t *testing.T) {
	if !slices.IsSorted(builtinFunctions) {
		t.Error("builtinFunctions is not sorted")
	}
	for name := range funcClass {
		if !builtinSet[name] {
			t.Errorf("funcClass names %s, which is not a built-in", name)
		}
	}
}

func TestReasonsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range append(slices.Clone(Reasons), HostReasons...) {
		if seen[r] {
			t.Errorf("%s twice", r)
		}
		seen[r] = true
		if !regexp.MustCompile(`^(light|focused|deep)-[a-z0-9-]+$`).MatchString(r) {
			t.Errorf("%s does not fit the token pattern", r)
		}
	}
}

func TestSafePath(t *testing.T) {
	for p, want := range map[string]bool{
		"a/b.md": true, "a;b": true, "a b/c": true,
		"": false, "/a": false, "a/../b": false, "a//b": false, "a/": false, `a\b`: false,
		"a\nb": false, "a\u202eb": false, "caf\u00e9.md": false,
	} {
		if got := safePath(p); got != want {
			t.Errorf("safePath(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestEveryReasonExercised runs every fixture and checks that each reason
// mtf-facts writes appears in at least one report.
func TestEveryReasonExercised(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		fx := loadFixture(t, f)
		for _, r := range fx.run(t).Reasons {
			seen[r] = true
		}
	}
	for _, r := range Reasons {
		if !seen[r] {
			t.Errorf("no fixture yields %s", r)
		}
	}
}
