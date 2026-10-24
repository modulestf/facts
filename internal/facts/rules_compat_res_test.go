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

func TestConstraintFloor(t *testing.T) {
	for c, want := range map[string]string{
		"":                  "0.0.0",
		">= 6.0":            "6.0.0",
		"~> 6.2":            "6.2.0",
		">= 6.0, < 7.0":     "6.0.0",
		"= 1.5.7":           "1.5.7",
		"1.5.7":             "1.5.7",
		">= 5.0, >= 5.40.1": "5.40.1",
		"<= 7.0":            "0.0.0",
		"> 6.0":             "unreadable",
		">= 6.0, != 6.0.0":  "unreadable",
		"latest":            "unreadable",
	} {
		got := "unreadable"
		if v, ok := constraintFloor(c); ok {
			got = v.String()
		}
		if got != want {
			t.Errorf("%q: got %s, want %s", c, got, want)
		}
	}
}

func TestInstanceKeys(t *testing.T) {
	for src, want := range map[string]string{
		`name = "x"`:                                   `[""]`,
		`count = 2`:                                    `["[0]" "[1]"]`,
		`count = var.create ? 1 : 0`:                   `["[0]"]`,
		`count = length(var.x)`:                        "unknown",
		`for_each = toset(["a", "b"])`:                 `["[\"a\"]" "[\"b\"]"]`,
		`for_each = var.on ? toset(["a"]) : toset([])`: `["[\"a\"]"]`,
		`for_each = { x = 1 }`:                         `["[\"x\"]"]`,
		`for_each = var.items`:                         "unknown",
		`count = 5000`:                                 "unknown",
	} {
		m, err := resParse(t, "resource \"aws_demo_widget\" \"this\" {\n  "+src+"\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		got := "unknown"
		if keys, ok := instanceKeys(m.Resources[0].Body); ok {
			got = fmt.Sprintf("%q", keys)
		}
		if got != want {
			t.Errorf("%s: got %s, want %s", src, got, want)
		}
	}
}

func resParse(t *testing.T, src string) (*Module, error) {
	t.Helper()
	return ParseDir(mustTree(t, writeTree(t, map[string]string{"main.tf": src})), ".", DefaultLimits)
}

// runChange runs the compat_res rules on a base and a head tree.
func runChange(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	r, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits}, compatResRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// cands lists the candidates of one rule as "file:line subject".
func cands(r *Report, rule string) []string {
	var out []string
	for _, c := range r.Candidates {
		if c.RuleID == rule {
			out = append(out, fmt.Sprintf("%s:%s %s", c.File, lineKey(c.Line), c.Subject))
		}
	}
	return out
}

const addrRule = "compat.resource-address-changed"

func TestResourceAddressCases(t *testing.T) {
	for name, tc := range map[string]struct {
		base, head string
		want       []string
	}{
		"count added keeps [0]": {
			base: "resource \"aws_a\" \"x\" {}\n",
			head: "resource \"aws_a\" \"x\" {\n  count = 1\n}\n",
		},
		"count removed keeps [0]": {
			base: "resource \"aws_a\" \"x\" {\n  count = var.on ? 1 : 0\n}\n",
			head: "resource \"aws_a\" \"x\" {}\n",
		},
		"same unknown repetition": {
			base: "resource \"aws_a\" \"x\" {\n  for_each = var.m\n  a = 1\n}\n",
			head: "resource \"aws_a\" \"x\" {\n  for_each = var.m\n  a = 2\n}\n",
		},
		"for_each widened": {
			base: "resource \"aws_a\" \"x\" {\n  for_each = toset([\"a\"])\n}\n",
			head: "resource \"aws_a\" \"x\" {\n  for_each = toset([\"a\", \"b\"])\n}\n",
		},
		"for_each narrowed": {
			base: "resource \"aws_a\" \"x\" {\n  for_each = toset([\"a\", \"b\"])\n}\n",
			head: "resource \"aws_a\" \"x\" {\n  for_each = toset([\"a\"])\n}\n",
			want: []string{"main.tf:2 .:aws_a.x"},
		},
		"expression key moved is undecidable": {
			base: "resource \"aws_a\" \"x\" {\n  count = 1\n}\n",
			head: "resource \"aws_a\" \"x\" {\n  for_each = var.m\n}\nmoved {\n  from = aws_a.x[0]\n  to   = aws_a.x[local.k]\n}\n",
			want: []string{"main.tf:2 .:aws_a.x"},
		},
		"whole address moved": {
			base: "resource \"aws_a\" \"x\" {\n  for_each = var.m\n}\n",
			head: "resource \"aws_a\" \"y\" {\n  for_each = var.m\n}\nmoved {\n  from = aws_a.x\n  to   = aws_a.y\n}\n",
		},
		"rename pairs with the added block": {
			base: "resource \"aws_a\" \"x\" {}\nresource \"aws_b\" \"x\" {}\n",
			head: "resource \"aws_b\" \"x\" {}\nresource \"aws_a\" \"y\" {}\n",
			want: []string{"main.tf:2 .:aws_a.y"},
		},
		"plain removal has no line": {
			base: "resource \"aws_a\" \"x\" {}\nresource \"aws_b\" \"x\" {}\n",
			head: "resource \"aws_b\" \"x\" {}\n",
			want: []string{"main.tf:null .:aws_a.x"},
		},
		"removed block is a candidate": {
			base: "resource \"aws_a\" \"x\" {}\n",
			head: "removed {\n  from = aws_a.x\n}\n",
			want: []string{"main.tf:1 .:aws_a.x"},
		},
		"whole move to other keys": {
			base: "resource \"aws_a\" \"x\" {\n  count = 1\n}\n",
			head: "resource \"aws_a\" \"y\" {\n  for_each = toset([\"a\"])\n}\nmoved {\n  from = aws_a.x\n  to   = aws_a.y\n}\n",
			want: []string{"main.tf:4 .:aws_a.x"},
		},
		"whole move to fewer keys": {
			base: "resource \"aws_a\" \"x\" {\n  count = 2\n}\n",
			head: "resource \"aws_a\" \"y\" {\n  count = 1\n}\nmoved {\n  from = aws_a.x\n  to   = aws_a.y\n}\n",
			want: []string{"main.tf:4 .:aws_a.x"},
		},
		"whole move keeps the implicit key": {
			base: "resource \"aws_a\" \"x\" {\n  count = 1\n}\n",
			head: "resource \"aws_a\" \"y\" {}\nmoved {\n  from = aws_a.x\n  to   = aws_a.y\n}\n",
		},
		"moved target missing": {
			base: "resource \"aws_a\" \"x\" {}\n",
			head: "moved {\n  from = aws_a.x\n  to   = aws_a.z\n}\n",
			want: []string{"main.tf:1 .:aws_a.x"},
		},
		"moved chain followed": {
			base: "resource \"aws_a\" \"x\" {}\n",
			head: "resource \"aws_a\" \"z\" {}\nmoved {\n  from = aws_a.x\n  to   = aws_a.y\n}\nmoved {\n  from = aws_a.y\n  to   = aws_a.z\n}\n",
		},
		"whole move into a module call": {
			base: "resource \"aws_a\" \"x\" {}\n",
			head: "module \"m\" {\n  source = \"./m\"\n}\nmoved {\n  from = aws_a.x\n  to   = module.m.aws_a.x\n}\n",
		},
		"instance move into a module call": {
			base: "resource \"aws_a\" \"x\" {\n  count = 1\n}\n",
			head: "module \"m\" {\n  source = \"./m\"\n}\nmoved {\n  from = aws_a.x[0]\n  to   = module.m.aws_a.x\n}\n",
			want: []string{"main.tf:4 .:aws_a.x"},
		},
		"module call renamed": {
			base: "module \"a\" {\n  source = \"./m\"\n}\n",
			head: "module \"b\" {\n  source = \"./m\"\n}\n",
			want: []string{"main.tf:1 .:module.b"},
		},
		"module call renamed with moved": {
			base: "module \"a\" {\n  source = \"./m\"\n}\n",
			head: "module \"b\" {\n  source = \"./m\"\n}\nmoved {\n  from = module.a\n  to   = module.b\n}\n",
		},
		"module call removed": {
			base: "module \"a\" {\n  source = \"./m\"\n}\nresource \"aws_b\" \"x\" {}\n",
			head: "resource \"aws_b\" \"x\" {}\n",
			want: []string{"main.tf:null .:module.a"},
		},
		"module call gains for_each": {
			base: "module \"a\" {\n  source = \"./m\"\n}\n",
			head: "module \"a\" {\n  source   = \"./m\"\n  for_each = toset([\"k\"])\n}\n",
			want: []string{"main.tf:3 .:module.a"},
		},
		"module call gains for_each with moved": {
			base: "module \"a\" {\n  source = \"./m\"\n}\n",
			head: "module \"a\" {\n  source   = \"./m\"\n  for_each = toset([\"k\"])\n}\nmoved {\n  from = module.a\n  to   = module.a[\"k\"]\n}\n",
		},
		"data block never counts": {
			base: "data \"aws_a\" \"x\" {}\nresource \"aws_b\" \"x\" {}\n",
			head: "resource \"aws_b\" \"x\" {}\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := runChange(t, map[string]string{"main.tf": tc.base}, map[string]string{"main.tf": tc.head})
			if got := cands(r, addrRule); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The pass reads an untouched area too: a parse error there makes the one
// root entry not_evaluated, with no candidate from the area in scope.
func TestResourceAddressCrossAreaUnparsable(t *testing.T) {
	base := map[string]string{"main.tf": "resource \"aws_a\" \"x\" {}\n", "modules/bad/main.tf": "resource {\n"}
	head := map[string]string{"main.tf": "", "modules/bad/main.tf": "resource {\n"}
	r := runChange(t, base, head)
	if s := statusOf(r.Manifest, addrRule, "."); !strings.HasPrefix(s, "not_evaluated") || len(cands(r, addrRule)) != 0 {
		t.Errorf("%s %q", s, cands(r, addrRule))
	}
}

const versionRule = "compat.version-constraint-raised"
const floorRule = "compat.provider-floor-raised"

func versionsTF(core, aws string) string {
	return "terraform {\n  required_version = \"" + core + "\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = " + aws + "\n    }\n  }\n}\n"
}

func TestVersionCases(t *testing.T) {
	for name, tc := range map[string]struct {
		base, head        string
		version, floorSts string
	}{
		"unchanged":           {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `">= 6.0"`), "", "missing"}, // no area in scope,
		"core lowered":        {versionsTF(">= 1.7.0", `">= 6.0"`), versionsTF(">= 1.5.7", `">= 6.0"`), "", "clean"},
		"core raised":         {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.7.0", `">= 6.0"`), "versions.tf:2 .:required_version", "clean"},
		"minor raise":         {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `">= 6.2"`), "", "not_evaluated: no schema handoff versions for provider aws"},
		"major raise":         {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `">= 7.0"`), "versions.tf:6 .:required_providers.aws", "clean"},
		"unreadable change":   {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `">= 6.0, != 6.1.0"`), "versions.tf:6 .:required_providers.aws", "not_evaluated: no schema handoff versions for provider aws"},
		"not a literal":       {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `local.v`), "versions.tf:6 .:required_providers.aws", "not_evaluated: no schema handoff versions for provider aws"},
		"same floor, new cap": {versionsTF(">= 1.5.7", `">= 6.0"`), versionsTF(">= 1.5.7", `"~> 6.0"`), "", "clean"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runChange(t, map[string]string{"versions.tf": tc.base}, map[string]string{"versions.tf": tc.head})
			got := strings.Join(cands(r, versionRule), ",")
			if got != tc.version {
				t.Errorf("version: got %q, want %q", got, tc.version)
			}
			if s := statusOf(r.Manifest, floorRule, "."); s != tc.floorSts {
				t.Errorf("floor: %s", s)
			}
		})
	}
}

// floorHandoff runs the compat-versions-floor oracle trees, with head
// changed by edit, and a copy of its handoff whose tree hashes match and
// whose versions edit sets.
func floorHandoff(t *testing.T, edit func(head map[string]string), versions func(v map[string]any), schemas ...func(h map[string]any)) *Report {
	t.Helper()
	src := filepath.Join(oracleRoot, "compat-versions-floor")
	read := func(rev string) map[string]string {
		out := map[string]string{}
		for _, n := range []string{"main.tf", "outputs.tf", "variables.tf", "versions.tf"} {
			b, err := os.ReadFile(filepath.Join(src, rev, n))
			if err != nil {
				t.Fatal(err)
			}
			out[n] = string(b)
		}
		return out
	}
	head := read("head")
	edit(head)
	hd, bd := writeTree(t, head), writeTree(t, read("base"))
	data, err := os.ReadFile(filepath.Join(src, "schema-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatal(err)
	}
	h["inputs"].(map[string]any)["head"].(map[string]any)["tree_hash"] = tfTreeHash(mustTree(t, hd))
	versions(h["directories"].([]any)[0].(map[string]any)["providers"].([]any)[0].(map[string]any))
	for _, e := range schemas {
		e(h)
	}
	out, _ := json.Marshal(h)
	hf := filepath.Join(t.TempDir(), "handoff.json")
	if err := os.WriteFile(hf, out, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run(Options{Head: hd, Base: bd, HeadSHA: oracleHeadSHA, MergeBaseSHA: oracleBaseSHA, SchemaHandoff: hf, Limits: DefaultLimits}, compatResRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProviderFloorWithSchema(t *testing.T) {
	same := func(map[string]string) {}
	// The handoff resolves the floors: the same old floor and minimum is no raise.
	r := floorHandoff(t, same, func(p map[string]any) { p["versions"].(map[string]any)["old_floor"] = "6.2.0" })
	if s := statusOf(r.Manifest, floorRule, "."); s != "clean" {
		t.Errorf("no raise: %s", s)
	}
	// An unresolved provider passes its reason on.
	r = floorHandoff(t, same, func(p map[string]any) { delete(p, "versions"); p["unresolved_reason"] = "constraint_unparsable" })
	if s := statusOf(r.Manifest, floorRule, "."); s != "not_evaluated: no schema handoff versions for provider aws: constraint_unparsable" {
		t.Errorf("unresolved: %s", s)
	}
	// Every name the module sets is present at both floors: no fact.
	r = floorHandoff(t, same, func(map[string]any) {})
	if len(r.Candidates) != 1 || len(r.Candidates[0].Evidence.SchemaFacts) != 0 || !r.Candidates[0].EvidenceComplete ||
		len(r.Candidates[0].Evidence.SourceLines) != 0 || *r.Candidates[0].Evidence.Base != ">= 6.0" || *r.Candidates[0].Evidence.Head != ">= 6.2" {
		t.Fatalf("all present: %+v", r.Candidates)
	}
	// A name the new floor adds is a fact at each floor; one the old floor
	// has and the new one deprecates too.
	r = floorHandoff(t, func(h map[string]string) {
		h["main.tf"] = strings.Replace(h["main.tf"], "  name = var.name\n", "  name = var.name\n  fresh = 1\n", 1)
	}, func(map[string]any) {}, func(h map[string]any) {
		for _, p := range h["provider_schemas"].([]any) {
			attrs := p.(map[string]any)["resource_schemas"].(map[string]any)["aws_demo_widget"].(map[string]any)["block"].(map[string]any)["attributes"].(map[string]any)
			if p.(map[string]any)["version"] == "6.2.0" {
				attrs["fresh"] = map[string]any{"optional": true}
				attrs["name"].(map[string]any)["deprecated"] = true
			}
		}
	})
	var got []string
	for _, f := range r.Candidates[0].Evidence.SchemaFacts {
		got = append(got, f.Version+" "+f.Path+" "+f.State)
	}
	if want := []string{"6.0.0 fresh absent", "6.2.0 fresh present", "6.0.0 name present", "6.2.0 name deprecated"}; !slices.Equal(got, want) {
		t.Errorf("facts %q, want %q", got, want)
	}
	// A name the new floor drops is a fact at each floor.
	r = floorHandoff(t, same, func(map[string]any) {}, func(h map[string]any) {
		for _, p := range h["provider_schemas"].([]any) {
			if p.(map[string]any)["version"] == "6.2.0" {
				delete(p.(map[string]any)["resource_schemas"].(map[string]any)["aws_demo_widget"].(map[string]any)["block"].(map[string]any)["attributes"].(map[string]any), "tags")
			}
		}
	})
	got = got[:0]
	for _, f := range r.Candidates[0].Evidence.SchemaFacts {
		got = append(got, f.Version+" "+f.Path+" "+f.State)
	}
	if want := []string{"6.0.0 tags present", "6.2.0 tags absent"}; !slices.Equal(got, want) {
		t.Errorf("dropped: facts %q, want %q", got, want)
	}
	// With no schema at the new floor only the old floor speaks.
	r = floorHandoff(t, same, func(map[string]any) {}, func(h map[string]any) {
		h["provider_schemas"] = slices.DeleteFunc(h["provider_schemas"].([]any), func(p any) bool { return p.(map[string]any)["version"] == "6.2.0" })
	})
	if len(r.Candidates) != 1 || len(r.Candidates[0].Evidence.SchemaFacts) != 0 {
		t.Fatalf("no new floor schema: %+v", r.Candidates)
	}
	// A type the old floor lacks is absent; too many facts are capped.
	r = floorHandoff(t, func(h map[string]string) {
		var b strings.Builder
		b.WriteString("resource \"aws_demo_unknown\" \"x\" {\n")
		for i := range maxResSchemaFacts + 1 {
			fmt.Fprintf(&b, "  a%d = 1\n", i)
		}
		h["main.tf"] += b.String() + "}\n"
	}, func(map[string]any) {})
	if len(r.Candidates) != 1 || r.Candidates[0].EvidenceComplete || len(r.Candidates[0].Evidence.SchemaFacts) != maxResSchemaFacts {
		t.Fatalf("cap: %+v", r.Candidates)
	}
	if f := r.Candidates[0].Evidence.SchemaFacts; f[0].Type != "aws_demo_unknown" || f[0].State != "absent" || f[len(f)-1].State != "absent" {
		t.Errorf("states %+v", f)
	}
}

const policyRule = "compat.generated-policy-narrowed"

func doc(stmts string) string {
	return "data \"aws_iam_policy_document\" \"p\" {\n" + stmts + "}\n"
}

func TestPolicyCases(t *testing.T) {
	allow := "  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n  }\n"
	for name, tc := range map[string]struct {
		base, head string
		want       []string
	}{
		"allow statement added": {doc(allow), doc(allow + allow), nil},
		"deny statement added": {doc(allow), doc(allow + "  statement {\n    effect  = \"Deny\"\n    actions = [\"a:Put\"]\n  }\n"),
			[]string{"main.tf:6 .:data.aws_iam_policy_document.p.statement[1]"}},
		"statement removed": {doc(allow + allow), doc(allow), []string{"main.tf:1 .:data.aws_iam_policy_document.p.statement[1]"}},
		"condition added": {doc(allow), doc("  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n    condition {\n      test     = \"StringEquals\"\n      variable = \"a:x\"\n      values   = [\"y\"]\n    }\n  }\n"),
			[]string{"main.tf:5 .:data.aws_iam_policy_document.p.statement[0].condition[0]"}},
		"effect changed": {doc(allow), doc("  statement {\n    effect    = \"Deny\"\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n  }\n"),
			[]string{"main.tf:3 .:data.aws_iam_policy_document.p.statement[0].effect"}},
		"dynamic statement edited": {doc("  dynamic \"statement\" {\n    for_each = var.s\n    content {\n      actions = statement.value\n    }\n  }\n"),
			doc("  dynamic \"statement\" {\n    for_each = var.t\n    content {\n      actions = statement.value\n    }\n  }\n"),
			[]string{"main.tf:2 .:data.aws_iam_policy_document.p.statement"}},
		"condition values grow": {doc("  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n    condition {\n      test     = \"StringEquals\"\n      variable = \"a:x\"\n      values   = [\"y\"]\n    }\n  }\n"), doc("  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n    condition {\n      test     = \"StringEquals\"\n      variable = \"a:x\"\n      values   = [\"y\", \"z\"]\n    }\n  }\n"), nil},
		"negated condition values grow": {doc("  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n    condition {\n      test     = \"StringNotEquals\"\n      variable = \"a:x\"\n      values   = [\"y\"]\n    }\n  }\n"), doc("  statement {\n    actions   = [\"a:Get\"]\n    resources = [\"*\"]\n    condition {\n      test     = \"StringNotEquals\"\n      variable = \"a:x\"\n      values   = [\"y\", \"z\"]\n    }\n  }\n"),
			[]string{"main.tf:8 .:data.aws_iam_policy_document.p.statement[0].condition[0].values"}},
		"principals grow": {doc("  statement {\n    actions = [\"a:Get\"]\n    principals {\n      type        = \"AWS\"\n      identifiers = [\"a\"]\n    }\n  }\n"), doc("  statement {\n    actions = [\"a:Get\"]\n    principals {\n      type        = \"AWS\"\n      identifiers = [\"a\", \"b\"]\n    }\n  }\n"), nil},
		"not_principals grow": {doc("  statement {\n    actions = [\"a:Get\"]\n    not_principals {\n      type        = \"AWS\"\n      identifiers = [\"a\"]\n    }\n  }\n"), doc("  statement {\n    actions = [\"a:Get\"]\n    not_principals {\n      type        = \"AWS\"\n      identifiers = [\"a\", \"b\"]\n    }\n  }\n"),
			[]string{"main.tf:6 .:data.aws_iam_policy_document.p.statement[0].not_principals[0].identifiers"}},
		"policy_document and _policies arguments": {"resource \"aws_r\" \"x\" {\n  policy_document  = \"a\"\n  managed_policies = [\"a\"]\n}\n", "resource \"aws_r\" \"x\" {\n  policy_document  = \"b\"\n  managed_policies = [\"b\"]\n}\n",
			[]string{"main.tf:2 .:aws_r.x.policy_document", "main.tf:3 .:aws_r.x.managed_policies"}},
		"inline policy in a nested block": {"resource \"aws_r\" \"x\" {\n  inline_policy {\n    policy = \"a\"\n  }\n}\n", "resource \"aws_r\" \"x\" {\n  inline_policy {\n    policy = \"b\"\n  }\n}\n",
			[]string{"main.tf:3 .:aws_r.x.inline_policy[0].policy"}},
		"policy removed from a block": {"resource \"aws_r\" \"x\" {\n  assume_role_policy = \"a\"\n}\n", "resource \"aws_r\" \"x\" {\n}\n",
			[]string{"main.tf:1 .:aws_r.x.assume_role_policy"}},
		"local through a local": {"locals {\n  a = local.b\n  b = [\"x\", \"y\"]\n}\nresource \"aws_r\" \"x\" {\n  policy = jsonencode(local.a)\n}\n",
			"locals {\n  a = local.b\n  b = [\"x\"]\n}\nresource \"aws_r\" \"x\" {\n  policy = jsonencode(local.a)\n}\n",
			[]string{"main.tf:3 .:local.b"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := runChange(t, map[string]string{"main.tf": tc.base}, map[string]string{"main.tf": tc.head})
			if got := cands(r, policyRule); !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResEvidenceCaps(t *testing.T) {
	long := strings.Repeat("x", maxEvidenceText+10)
	r := runChange(t, map[string]string{"main.tf": "resource \"aws_r\" \"x\" {\n  policy = \"a\"\n}\n"},
		map[string]string{"main.tf": "resource \"aws_r\" \"x\" {\n  policy = \"" + long + "\"\n}\n"})
	c := r.Candidates
	if len(c) != 1 || c[0].EvidenceComplete || len(*c[0].Evidence.Head) != maxEvidenceText {
		t.Errorf("%+v", c)
	}
	e := newResEvidence(&Area{Path: ".", headTree: mustTree(t, writeTree(t, map[string]string{"a.tf": strings.Repeat("l\n", 20)}))})
	for i := 1; i <= maxEvidenceLines+1; i++ {
		e.line("head", "a.tf", i)
	}
	if len(e.ev.SourceLines) != maxEvidenceLines || e.complete {
		t.Errorf("source lines %d complete %v", len(e.ev.SourceLines), e.complete)
	}
}

// A module new in the change, with an empty base, breaks no caller.
func TestCompatResNewModule(t *testing.T) {
	r := runChange(t, map[string]string{}, map[string]string{"versions.tf": versionsTF(">= 1.9.0", `">= 7.0"`)})
	for _, e := range r.Manifest {
		if e.Status != StatusClean {
			t.Errorf("%+v", e)
		}
	}
}
