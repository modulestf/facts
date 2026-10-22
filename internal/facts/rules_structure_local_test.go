package facts

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// localKeys runs a change with the test profile and lists, for the structure
// local rules, each manifest status other than clean and not_applicable as
// "<rule> <area> <status>" and each candidate as "<rule> <file>:<line> <subject>".
func localKeys(t *testing.T, base, head map[string]string, profile bool) []string {
	t.Helper()
	o := Options{Head: writeTree(t, head), Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	if profile {
		o.ProfileDir = testProfile
	}
	r, err := run(o, structureLocalRules)
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
		out = append(out, fmt.Sprintf("%s %s:%d %s", c.RuleID, c.File, *c.Line, c.Subject))
	}
	slices.Sort(out)
	return out
}

func TestStructureLocalHardCases(t *testing.T) {
	const widget = "resource \"aws_demo_widget\" \"this\" {\n  count = 1\n\n  name = var.name\n\n  tags = var.tags\n}\n"
	for _, tt := range []struct {
		name       string
		base, head map[string]string
		noProfile  bool
		want       []string
	}{
		{
			name: "argument order: for_each late, tags before a block, a data block ignored",
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"a\" {\n  name     = var.name\n  for_each = var.items\n}\n\n" +
				"resource \"aws_demo_widget\" \"b\" {\n  tags = var.tags\n\n  lifecycle {\n    create_before_destroy = true\n  }\n}\n\n" +
				"data \"aws_demo_widget\" \"c\" {\n  name  = var.name\n  count = 1\n}\n\n" + widget},
			want: []string{"structure.argument-order main.tf:1 .:aws_demo_widget.a", "structure.argument-order main.tf:6 .:aws_demo_widget.b"},
		},
		{
			name: "argument order: dynamic block before tags, labels last, both first arguments first",
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"a\" {\n  count    = 1\n  for_each = {}\n  name     = var.name\n" +
				"  dynamic \"rule\" {\n    for_each = var.rules\n    content {}\n  }\n  tags   = var.tags\n  labels = var.labels\n}\n"},
		},
		{
			name: "argument order: only touched blocks, an untouched bad block is left out",
			base: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"old\" {\n  name  = var.name\n  count = 1\n}\n\n" +
				"resource \"aws_demo_widget\" \"edited\" {\n  count = 1\n  tags  = var.tags\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"old\" {\n  name  = var.name\n  count = 1\n}\n\n" +
				"resource \"aws_demo_widget\" \"edited\" {\n  count = 1\n  tags  = var.tags\n  name  = var.name\n}\n"},
			want: []string{"structure.argument-order main.tf:6 .:aws_demo_widget.edited"},
		},
		{
			name:      "argument order: no profile",
			head:      map[string]string{"main.tf": widget},
			noProfile: true,
			want:      []string{"structure.argument-order . not_evaluated profile_unavailable"},
		},
		{
			name: "arn: comments, word prefixes and derived parts are silent",
			head: map[string]string{"main.tf": "# arn:aws:iam::123456789012:role/x\nresource \"aws_demo_widget\" \"this\" {\n" +
				"  name = \"learn:aws:a:b:c:d\" # arn:aws:s3:::x\n" +
				"  arn  = \"arn:${local.partition}:iam::${local.account}:role/x\"\n" +
				"  s3   = \"arn:${local.partition}:s3:::${var.bucket}\"\n" +
				"  prefix = \"arn:aws\"\n}\n"},
		},
		{
			name: "arn: a literal partition, region and account each fire; nested, dynamic and local subjects",
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n" +
				"  a = \"arn:aws-cn:s3:::x\"\n" +
				"  b = \"arn:${local.p}:sns:us-gov-west-1:${local.acct}:t\"\n" +
				"  c = jsonencode({ Resource = \"arn:${local.p}:iam::123456789012:root\" })\n" +
				"  dynamic \"rule\" {\n    for_each = [\"arn:aws:s3:::y\"]\n    content {}\n  }\n" +
				"  block {\n    d = \"arn:aws:s3:::z\"\n  }\n}\n\n" +
				"locals {\n  role = \"arn:aws:iam::aws:policy/ReadOnlyAccess\"\n}\n"},
			want: []string{
				"structure.arn-partition-hardcoded main.tf:10 .:aws_demo_widget.this.block[0].d",
				"structure.arn-partition-hardcoded main.tf:15 .:local.role",
				"structure.arn-partition-hardcoded main.tf:2 .:aws_demo_widget.this.a",
				"structure.arn-partition-hardcoded main.tf:3 .:aws_demo_widget.this.b",
				"structure.arn-partition-hardcoded main.tf:4 .:aws_demo_widget.this.c",
				"structure.arn-partition-hardcoded main.tf:6 .:aws_demo_widget.this.rule",
			},
		},
		{
			name: "arn: an ARN added to an existing block is not newly introduced",
			base: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name = var.name\n}\n"},
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name = var.name\n  arn  = \"arn:aws:s3:::x\"\n}\n"},
		},
		{
			name: "multi-provider: a provider without alias, and an example, are silent",
			head: map[string]string{
				"main.tf":                "provider \"aws\" {\n  region = \"eu-west-1\"\n}\n",
				"modules/m/main.tf":      "provider \"aws\" {\n  alias = local.alias\n}\n",
				"examples/e/main.tf":     "provider \"aws\" {\n  alias = \"x\"\n}\nmodule \"m\" {\n  source    = \"../..\"\n  providers = { aws = aws.x }\n}\n",
				"examples/e/versions.tf": "terraform {}\n",
			},
			want: []string{"structure.multi-provider modules/m/main.tf:1 modules/m:provider.aws"},
		},
		{
			name: "output: null fallbacks, input-only values and untouched outputs are silent",
			base: map[string]string{"outputs.tf": "output \"old\" {\n  value = aws_demo_widget.this[0].id\n}\n"},
			head: map[string]string{"outputs.tf": "output \"old\" {\n  value = aws_demo_widget.this[0].id\n}\n" +
				"output \"a\" {\n  value = try(aws_demo_widget.this[0].id, null)\n}\n" +
				"output \"b\" {\n  value = var.name\n}\n" +
				"output \"c\" {\n  value = \"fixed\"\n}\n" +
				"output \"d\" {\n  value = { x = try(module.m.x, null), y = try(local.y, null) }\n}\n" +
				"output \"e\" {\n  value = { x = try(module.m.x, null), y = local.y }\n}\n" +
				"output \"f\" {\n  value = try(local.y)\n}\n"},
			want: []string{"structure.output-no-try outputs.tf:19 .:output.f"},
		},
		{
			name: "wrong file: no archetype file, a block in its file, a symbolic link is not the file",
			head: map[string]string{
				"main.tf":           "resource \"aws_iam_role\" \"a\" {}\nlocals {\n  x = 1\n}\n",
				"iam.tf":            "resource \"aws_iam_role\" \"b\" {}\n",
				"modules/m/main.tf": "resource \"aws_iam_role\" \"c\" {}\nresource \"aws_cloudwatch_log_group\" \"d\" {}\n",
				"modules/m/iam.tf":  "->../../iam.tf",
			},
			want: []string{
				"structure.argument-order modules/m not_evaluated symlink",
				"structure.arn-partition-hardcoded modules/m not_evaluated symlink",
				"structure.multi-provider modules/m not_evaluated symlink",
				"structure.output-no-try modules/m not_evaluated symlink",
				"structure.wrong-file main.tf:1 .:aws_iam_role.a",
				"structure.wrong-file modules/m not_evaluated symlink",
			},
		},
		{
			name: "wrong file: logging and locals groups",
			head: map[string]string{
				"main.tf":    "resource \"aws_cloudwatch_log_group\" \"a\" {}\nlocals {\n  x = 1\n}\n",
				"logging.tf": "",
				"locals.tf":  "locals {\n  y = 2\n}\n",
			},
			want: []string{"structure.wrong-file main.tf:1 .:aws_cloudwatch_log_group.a", "structure.wrong-file main.tf:3 .:local.x"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := localKeys(t, tt.base, tt.head, !tt.noProfile)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

func TestStructureLocalEvidenceCap(t *testing.T) {
	var args strings.Builder
	for i := range 200 {
		fmt.Fprintf(&args, "  argument_%03d = 1\n", i)
	}
	r, err := Run(Options{Head: writeTree(t, map[string]string{
		"main.tf": "resource \"aws_demo_widget\" \"this\" {\n" + args.String() + "  count = 1\n}\n",
	}), Limits: DefaultLimits, ProfileDir: testProfile})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(r.Candidates, func(c Candidate) bool { return c.RuleID == "structure.argument-order" })
	if i < 0 {
		t.Fatalf("no candidate: %+v", r.Candidates)
	}
	c := r.Candidates[i]
	if c.EvidenceComplete || len(*c.Evidence.Head) != maxEvidenceText || c.Evidence.Base != nil {
		t.Errorf("cap did not bite: complete=%v len=%d", c.EvidenceComplete, len(*c.Evidence.Head))
	}
}
