package facts

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
)

// everyFeature uses every row of the core feature table once, and nothing
// the table does not list.
const everyFeature = `terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      configuration_aliases = [aws.x]
    }
  }
}

variable "v" {
  type       = object({ a = optional(string), b = optional(string, "x") })
  sensitive  = true
  nullable   = false
  ephemeral  = true
  deprecated = "d"
  validation {
    condition     = var.other == null
    error_message = "e"
  }
}

output "o" {
  value      = 1
  sensitive  = true
  ephemeral  = true
  deprecated = "d"
  type       = string
  precondition {
    condition     = true
    error_message = "e"
  }
}

locals {
  f = [sum([1]), alltrue([]), anytrue([]), textencodebase64("a", "UTF-8"), textdecodebase64("a", "UTF-8"), one([]),
    sensitive(1), nonsensitive(1), startswith("a", "b"), endswith("a", "b"), timecmp("a", "b"), plantimestamp(),
    strcontains("a", "b"), issensitive(1), templatestring("a", {}), ephemeralasnull(1), convert(1, number),
    provider::terraform::encode_tfvars({}), provider::terraform::decode_tfvars(""), provider::terraform::encode_expr(1),
  provider::aws::arn_parse("x")]
}

resource "terraform_data" "d" {
  store {}
  lifecycle {
    replace_triggered_by = []
    destroy              = false
    precondition {
      condition     = true
      error_message = "e"
    }
    postcondition {
      condition     = true
      error_message = "e"
    }
    action_trigger {
      events     = [before_destroy]
      on_failure = halt
    }
  }
}

module "m" {
  source     = var.src
  count      = 1
  for_each   = {}
  depends_on = []
}

moved {
  from = null_resource.a
  to   = terraform_data.b
}

check "c" {
  assert {
    condition     = true
    error_message = "e"
  }
}

import {
  to       = aws_x.y
  id       = "i"
  for_each = {}
  identity = {}
}

removed {
  from = aws_x.z
  provisioner "local-exec" {
    command = "x"
  }
}

ephemeral "aws_x" "e" {}

action "aws_x" "a" {}
`

// Every row of the table has a detector, and the detectors name no row the
// table lacks: feature panics on one.
func TestCoreFeatureTableIsDetected(t *testing.T) {
	m, err := ParseDir(mustTree(t, writeTree(t, map[string]string{"main.tf": everyFeature})), ".", DefaultLimits)
	if err != nil || len(m.Unsupported) > 0 {
		t.Fatalf("parse: %v %v", err, m.Unsupported)
	}
	got := map[string]bool{}
	for _, u := range featureUses(m) {
		if u.row == nil {
			t.Errorf("unlisted feature at line %d: %s", u.line, u.text)
			continue
		}
		got[u.row.ID] = true
	}
	want := slices.Sorted(maps.Keys(coreFeatures))
	if g := slices.Sorted(maps.Keys(got)); !slices.Equal(g, want) {
		t.Errorf("detected rows\n got %v\nwant %v", g, want)
	}
}

func TestLoadCoreFeaturesRefusesBadRows(t *testing.T) {
	for _, row := range []string{
		`{"id":"x","kind":"block","match":"m","introduced":"1.2","source":"https://e"}`,
		`{"id":"x","kind":"blocks","match":"m","introduced":"1.2.0","source":"https://e"}`,
		`{"id":"x","kind":"block","match":"m","introduced":"1.2.0","source":"http://e"}`,
		`{"id":"x","kind":"block","match":"m","introduced":"1.2.0","source":"https://e","extra":1}`,
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("no panic on %s", row)
				}
			}()
			loadCoreFeatures([]byte("[" + row + "]"))
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("no panic on a duplicate id")
			}
		}()
		r := `{"id":"x","kind":"block","match":"m","introduced":"1.2.0","source":"https://e"}`
		loadCoreFeatures([]byte("[" + r + "," + r + "]"))
	}()
}

// The floor examples of data/README.md, and sets that admit no release.
func TestLowestAdmitted(t *testing.T) {
	for _, tt := range []struct{ constraint, want string }{
		{">= 1.3", "1.3.0"}, {">= 1.3.0", "1.3.0"}, {"~> 1.3", "1.3.0"}, {"~> 1.3.2", "1.3.2"},
		{">= 1.3, < 2.0", "1.3.0"}, {">= 1.3, != 1.3.0", "1.3.1"}, {">= 1.3, != 1.3.0, != 1.3.1", "1.3.2"},
		{"= 1.5.7", "1.5.7"}, {"1.5.7", "1.5.7"}, {"> 1.3", "1.3.1"}, {"< 1.0", "0.0.0"},
		{">= 2.0, < 1.0", "none"}, {"= 1.5.7, != 1.5.7", "none"},
	} {
		cs, err := version.NewConstraint(tt.constraint)
		if err != nil {
			t.Fatal(err)
		}
		got := "none"
		if v := lowestAdmitted(cs); v != nil {
			got = v.String()
		}
		if got != tt.want {
			t.Errorf("%q: got %s, want %s", tt.constraint, got, tt.want)
		}
	}
}

// versionKeys runs a change with the structure version rules and lists each
// manifest status other than clean, not_applicable and candidate as
// "<rule> <area> <status> <code>", and each candidate as
// "<rule> <file>:<line> <subject>". A nil base runs with no base.
func versionKeys(t *testing.T, base, head map[string]string) []string {
	t.Helper()
	o := Options{Head: writeTree(t, head), Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	r, err := run(o, structureVersionsRules)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range r.Manifest {
		if e.Status != StatusClean && e.Status != StatusNotApplicable && e.Status != StatusCandidate {
			out = append(out, strings.Join([]string{e.RuleID, e.Area, e.Status, e.ReasonCode}, " "))
		}
	}
	for _, c := range r.Candidates {
		line := "null"
		if c.Line != nil {
			line = fmt.Sprint(*c.Line)
		}
		out = append(out, fmt.Sprintf("%s %s:%s %s", c.RuleID, c.File, line, c.Subject))
	}
	slices.Sort(out)
	return out
}

func requires(constraint string) string {
	return "terraform {\n  required_version = \"" + constraint + "\"\n}\n"
}

const (
	featureRule    = "structure.feature-above-version-floor"
	understateRule = "structure.version-floor-understated"
	defaultRule    = "structure.passthrough-default-drift"
	fieldRule      = "structure.passthrough-field-drift"
)

func TestFeatureAboveFloorHardCases(t *testing.T) {
	for _, tt := range []struct {
		name       string
		base, head map[string]string
		want       []string
	}{
		{
			name: "a function the table does not list, the core:: namespace and a provider function under 1.8",
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "main.tf": "locals {\n" +
				"  a = cidrcontains(\"10.0.0.0/8\", \"10.1.0.0\")\n  b = core::upper(\"x\")\n" +
				"  c = provider::aws::arn_parse(\"x\")\n  d = upper(try(var.x, \"y\"))\n  e = strcontains(\"ab\", \"a\")\n}\n"},
			want: []string{featureRule + " main.tf:2 .:local.a", featureRule + " main.tf:3 .:local.b", featureRule + " main.tf:4 .:local.c"},
		},
		{
			name: "the floor reaches the feature, down to the patch",
			head: map[string]string{"versions.tf": requires(">= 1.8, != 1.8.0"), "main.tf": "locals {\n" +
				"  c = provider::aws::arn_parse(\"x\")\n  t = provider::terraform::encode_expr(1)\n}\n"},
		},
		{
			name: "a construct that matches two rows takes the higher",
			head: map[string]string{"versions.tf": requires(">= 1.8.0"), "main.tf": "import {\n  to = aws_x.y\n  id = \"i\"\n}\n\n" +
				"moved {\n  from = null_resource.a\n  to   = terraform_data.a\n}\n\nmoved {\n  from = aws_x.a\n  to   = aws_y.a\n}\n"},
			want: []string{featureRule + " main.tf:1 .:import", featureRule + " main.tf:6 .:moved.null_resource.a"},
		},
		{
			name: "optional() in a type, and the type argument of convert(), which is no unlisted function",
			head: map[string]string{"versions.tf": requires("~> 1.2.0"), "variables.tf": "variable \"v\" {\n" +
				"  type = object({\n    a = optional(string)\n    b = list(map(set(tuple([string, number, bool, any]))))\n  })\n}\n",
				"main.tf": "locals {\n  x = convert(1, object({ a = string }))\n}\n"},
			want: []string{featureRule + " main.tf:2 .:local.x", featureRule + " variables.tf:3 .:var.v.a"},
		},
		{
			name: "an expression the change does not touch is out of scope",
			base: map[string]string{"versions.tf": requires(">= 1.0"), "main.tf": "locals {\n  a = templatestring(\"x\", {})\n}\n"},
			head: map[string]string{"versions.tf": requires(">= 1.0"), "main.tf": "locals {\n  a = templatestring(\"x\", {})\n  b = 1\n}\n"},
		},
		{
			name: "no required_version: every row is a candidate, a feature older than every row is not",
			head: map[string]string{"variables.tf": "variable \"a\" {\n  type      = string\n  sensitive = true\n}\n\nvariable \"b\" {\n  type = string\n}\n"},
			want: []string{featureRule + " variables.tf:3 .:var.a"},
		},
		{
			name: "a floor below the table baseline: every touched block",
			head: map[string]string{"versions.tf": requires(">= 0.12"), "main.tf": "locals {\n  x = 1\n}\n"},
			want: []string{featureRule + " main.tf:2 .:local.x", featureRule + " versions.tf:1 .:terraform"},
		},
		{
			name: "a constraint that is not a plain string: every touched block",
			head: map[string]string{"versions.tf": "terraform {\n  required_version = \">= ${local.v}\"\n}\n", "main.tf": "variable \"x\" {\n  type = string\n}\n"},
			want: []string{featureRule + " main.tf:1 .:var.x", featureRule + " versions.tf:1 .:terraform"},
		},
		{
			name: "a block or argument Terraform does not have",
			head: map[string]string{"versions.tf": "terraform {\n  required_version = \">= 1.5.7\"\n  cloud {}\n}\n",
				"main.tf": "foo \"x\" {}\n\nvariable \"x\" {\n  type  = string\n  extra = 1\n}\n"},
			want: []string{featureRule + " main.tf:1 .:foo.x", featureRule + " main.tf:5 .:var.x", featureRule + " versions.tf:3 .:cloud"},
		},
		{
			name: "a module source read from a local",
			head: map[string]string{"versions.tf": requires(">= 1.14"), "main.tf": "locals {\n  src = \"./modules/x\"\n}\n\nmodule \"m\" {\n  source = local.src\n}\n"},
			want: []string{featureRule + " main.tf:6 .:module.m.source"},
		},
		{
			name: "a parse error is not evaluated",
			head: map[string]string{"main.tf": "locals {\n"},
			want: []string{featureRule + " . not_evaluated parse_error", defaultRule + " . not_evaluated parse_error",
				fieldRule + " . not_evaluated parse_error", understateRule + " . not_evaluated parse_error"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := versionKeys(t, tt.base, tt.head); !slices.Equal(got, tt.want) {
				t.Errorf("\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestFeatureEvidenceCap(t *testing.T) {
	long := strings.Repeat("x", maxEvidenceText+10)
	r, err := run(Options{Head: writeTree(t, map[string]string{"versions.tf": requires(">= 1.5.7"),
		"main.tf": "locals {\n  a = templatestring(\"" + long + "\", {})\n}\n"}), Limits: DefaultLimits}, structureVersionsRules)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].EvidenceComplete || len(*r.Candidates[0].Evidence.Head) != maxEvidenceText {
		t.Errorf("cap did not bite: %+v", r.Candidates)
	}
}

func providers(rv, aws string) string {
	return "terraform {\n  required_version = \"" + rv + "\"\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n" +
		"      version = " + aws + "\n    }\n    random = {\n      source  = \"hashicorp/random\"\n      version = \">= 3.0\"\n    }\n  }\n}\n"
}

func TestVersionFloorUnderstatedHardCases(t *testing.T) {
	call := func(source string) string { return "module \"this\" {\n  source = \"" + source + "\"\n}\n" }
	for _, tt := range []struct {
		name       string
		base, head map[string]string
		want       []string
	}{
		{
			name: "disjoint sets",
			base: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.5.7")},
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.0, < 1.5")},
			want: []string{understateRule + " examples/e/versions.tf:2 examples/e:required_version"},
		},
		{
			name: "the callee raises its floor and the caller is untouched: the candidate lands on the raise",
			base: map[string]string{"versions.tf": requires(">= 1.0"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.0")},
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.0")},
			want: []string{understateRule + " versions.tf:2 examples/e:required_version"},
		},
		{
			name: "a new call to a module with a higher floor lands on the call",
			base: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": "", "examples/e/versions.tf": requires(">= 1.0")},
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.0")},
			want: []string{understateRule + " examples/e/main.tf:2 examples/e:required_version"},
		},
		{
			name: "no required_version in the caller, a higher caller, a registry call and an unchanged pair are silent",
			base: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/old/main.tf": call("../../"), "examples/old/versions.tf": requires(">= 1.0")},
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/old/main.tf": call("../../"), "examples/old/versions.tf": requires(">= 1.0"),
				"examples/none/main.tf": call("../../"), "examples/high/main.tf": call("../../"), "examples/high/versions.tf": requires(">= 1.6"),
				"examples/reg/main.tf": call("terraform-aws-modules/x/aws"), "examples/reg/versions.tf": requires(">= 0.12")},
		},
		{
			name: "submodules call submodules",
			head: map[string]string{"versions.tf": requires(">= 1.0"), "main.tf": call("./modules/a"),
				"modules/a/versions.tf": requires(">= 1.3"), "modules/a/main.tf": call("../b"), "modules/b/versions.tf": requires(">= 1.5")},
			want: []string{understateRule + " modules/a/versions.tf:2 modules/a:required_version", understateRule + " versions.tf:2 .:required_version"},
		},
		{
			name: "disjoint sets where the caller's floor is the higher",
			head: map[string]string{"versions.tf": requires("~> 1.4.0"), "examples/e/main.tf": call("../../"), "examples/e/versions.tf": requires(">= 1.6")},
			want: []string{understateRule + " examples/e/versions.tf:2 examples/e:required_version"},
		},
		{
			name: "a provider floor below the callee's, compared per source address",
			head: map[string]string{"versions.tf": providers(">= 1.5.7", "\">= 6.0\""), "main.tf": call("./modules/a"),
				"modules/a/versions.tf": strings.Replace(providers(">= 1.5.7", "\">= 6.42\""), "\"hashicorp/aws\"", "\"registry.terraform.io/HashiCorp/aws\"", 1)},
			want: []string{understateRule + " versions.tf:6 .:required_providers.aws"},
		},
		{
			name: "a provider version that is not a plain string",
			head: map[string]string{"versions.tf": providers(">= 1.5.7", "var.v"), "main.tf": call("./modules/a"),
				"modules/a/versions.tf": providers(">= 1.5.7", "\">= 6.0\"")},
			want: []string{understateRule + " versions.tf:6 .:required_providers.aws"},
		},
		{
			name: "a root with no required_version calls a submodule with a floor",
			head: map[string]string{"main.tf": call("./modules/a"), "modules/a/versions.tf": requires(">= 1.3")},
			want: []string{understateRule + " modules/a/versions.tf:2 .:required_version"},
		},
		{
			name: "a constraint that is not a plain string is a candidate",
			head: map[string]string{"versions.tf": requires(">= 1.5.7"), "examples/e/main.tf": call("../../"),
				"examples/e/versions.tf": "terraform {\n  required_version = local.v\n}\n"},
			want: []string{understateRule + " examples/e/versions.tf:2 examples/e:required_version"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.DeleteFunc(versionKeys(t, tt.base, tt.head), func(k string) bool { return strings.HasPrefix(k, featureRule) })
			if !slices.Equal(got, tt.want) {
				t.Errorf("\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestPassthroughHardCases(t *testing.T) {
	const sub = "variable \"size\" {\n  type    = number\n  default = 20\n}\n"
	for _, tt := range []struct {
		name       string
		base, head map[string]string
		want       []string
	}{
		{
			name: "a new call forwarding an unchanged variable lands on the forwarding line",
			base: map[string]string{"variables.tf": "variable \"size\" {\n  type    = number\n  default = 10\n}\n", "modules/c/variables.tf": sub},
			head: map[string]string{"variables.tf": "variable \"size\" {\n  type    = number\n  default = 10\n}\n", "modules/c/variables.tf": sub,
				"main.tf": "module \"c\" {\n  source = \"./modules/c\"\n  size   = var.size\n}\n"},
			want: []string{defaultRule + " main.tf:3 .:var.size"},
		},
		{
			name: "a changed submodule default lands on the submodule's line",
			base: map[string]string{"variables.tf": "variable \"size\" {\n  type    = number\n  default = 20\n}\n", "modules/c/variables.tf": sub,
				"main.tf": "module \"c\" {\n  source = \"./modules/c\"\n  size   = var.size\n}\n"},
			head: map[string]string{"variables.tf": "variable \"size\" {\n  type    = number\n  default = 20\n}\n",
				"modules/c/variables.tf": "variable \"size\" {\n  type    = number\n  default = 30\n}\n",
				"main.tf":                "module \"c\" {\n  source = \"./modules/c\"\n  size   = var.size\n}\n"},
			want: []string{defaultRule + " modules/c/variables.tf:3 .:var.size"},
		},
		{
			name: "each.value of a for_each over a root variable",
			head: map[string]string{
				"variables.tf": "variable \"items\" {\n  type = map(object({\n    name = string\n    mode = optional(string, \"a\")\n  }))\n  default = {}\n}\n",
				"main.tf": "locals {\n  items = { for k, v in var.items : k => v if v.name != \"\" }\n}\n\n" +
					"module \"c\" {\n  source   = \"./modules/c\"\n  for_each = local.items\n  name     = each.value.name\n  mode     = each.value.mode\n}\n",
				"modules/c/variables.tf": "variable \"name\" {\n  type = string\n}\n\nvariable \"mode\" {\n  type    = string\n  default = \"b\"\n}\n\n" +
					"variable \"tags\" {\n  type    = map(string)\n  default = {}\n}\n"},
			want: []string{defaultRule + " variables.tf:4 .:var.items.mode", fieldRule + " variables.tf:2 .:var.items"},
		},
		{
			name: "a ternary over two inputs, an attribute the submodule lacks, and what is not forwarding",
			head: map[string]string{
				"variables.tf": "variable \"a\" {\n  type    = object({ x = optional(string), extra = optional(string) })\n  default = null\n}\n\n" +
					"variable \"l\" {\n  type    = list(string)\n  default = []\n}\n",
				"main.tf": "module \"c\" {\n  source   = \"./modules/c\"\n  settings = var.a != null ? var.a : var.a\n" +
					"  first    = var.l[0]\n  name     = \"x\"\n  merged   = merge(var.a, {})\n}\n",
				"modules/c/variables.tf": "variable \"settings\" {\n  type    = object({ x = optional(string) })\n  default = {}\n}\n\n" +
					"variable \"first\" {\n  type    = string\n  default = \"f\"\n}\n\nvariable \"name\" {\n  type    = string\n  default = \"n\"\n}\n\n" +
					"variable \"merged\" {\n  type    = any\n  default = {}\n}\n"},
			want: []string{defaultRule + " variables.tf:1 .:var.a", fieldRule + " variables.tf:2 .:var.a.extra"},
		},
		{
			name: "a required object, a string index, and a new call that sets no other variable",
			base: map[string]string{"variables.tf": "variable \"settings\" {\n  type = object({\n    mode = optional(string, \"a\")\n  })\n}\n",
				"modules/c/variables.tf": "variable \"mode\" {\n  type    = string\n  default = \"b\"\n}\n\nvariable \"tags\" {\n  type    = map(string)\n  default = {}\n}\n"},
			head: map[string]string{"variables.tf": "variable \"settings\" {\n  type = object({\n    mode = optional(string, \"a\")\n  })\n}\n",
				"modules/c/variables.tf": "variable \"mode\" {\n  type    = string\n  default = \"b\"\n}\n\nvariable \"tags\" {\n  type    = map(string)\n  default = {}\n}\n",
				"main.tf":                "module \"c\" {\n  source = \"./modules/c\"\n  mode   = var.settings[\"mode\"]\n}\n"},
			want: []string{defaultRule + " main.tf:3 .:var.settings.mode", fieldRule + " main.tf:2 .:var.settings"},
		},
		{
			name: "a shape mismatch, an explicit nullable = true, and several references",
			head: map[string]string{
				"variables.tf": "variable \"cfg\" {\n  type    = string\n  default = \"x\"\n}\n\nvariable \"label\" {\n  type    = string\n  default = null\n}\n\n" +
					"variable \"a\" {\n  type    = number\n  default = 20\n}\n\nvariable \"b\" {\n  type    = number\n  default = 20\n}\n",
				"main.tf": "module \"c\" {\n  source = \"./modules/c\"\n  cfg    = var.cfg\n  label  = var.label\n  size   = coalesce(var.a, var.b)\n}\n",
				"modules/c/variables.tf": "variable \"cfg\" {\n  type    = object({ a = optional(string) })\n  default = {}\n}\n\n" +
					"variable \"label\" {\n  type     = string\n  default  = \"c\"\n  nullable = true\n}\n\n" + sub},
			want: []string{defaultRule + " variables.tf:1 .:var.cfg", defaultRule + " variables.tf:11 .:var.a", defaultRule + " variables.tf:16 .:var.b",
				defaultRule + " variables.tf:6 .:var.label", fieldRule + " variables.tf:2 .:var.cfg"},
		},
		{
			name: "a registry call and a root null into a non-nullable variable are silent",
			head: map[string]string{
				"variables.tf": "variable \"label\" {\n  type    = string\n  default = null\n}\n",
				"main.tf": "module \"c\" {\n  source = \"./modules/c\"\n  label  = var.label\n}\n\n" +
					"module \"r\" {\n  source = \"terraform-aws-modules/x/aws\"\n  label  = var.label\n}\n",
				"modules/c/variables.tf": "variable \"label\" {\n  type     = string\n  default  = \"c\"\n  nullable = false\n}\n"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.DeleteFunc(versionKeys(t, tt.base, tt.head), func(k string) bool {
				return strings.HasPrefix(k, featureRule) || strings.HasPrefix(k, understateRule)
			})
			if !slices.Equal(got, tt.want) {
				t.Errorf("\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
