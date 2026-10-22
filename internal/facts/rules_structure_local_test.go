package facts

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
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
			name: "output: every traversal outside try() but a bare var is a candidate; null fallbacks and untouched outputs are silent",
			base: map[string]string{"outputs.tf": "output \"old\" {\n  value = aws_demo_widget.this[0].id\n}\n"},
			head: map[string]string{"outputs.tf": "output \"old\" {\n  value = aws_demo_widget.this[0].id\n}\n" +
				"output \"a\" {\n  value = try(aws_demo_widget.this[0].id, null)\n}\n" +
				"output \"b\" {\n  value = var.name\n}\n" +
				"output \"c\" {\n  value = \"fixed\"\n}\n" +
				"output \"d\" {\n  value = { x = try(module.m.x, null), y = try(local.y, null) }\n}\n" +
				"output \"e\" {\n  value = { x = try(module.m.x, null), y = local.y }\n}\n" +
				"output \"f\" {\n  value = try(local.y)\n}\n" +
				"output \"g\" {\n  value = { id = aws_demo_widget.this[0].id, name = try(aws_demo_widget.this[0].name, null) }\n}\n" +
				"output \"h\" {\n  value = var.config.name\n}\n" +
				"output \"i\" {\n  value = var.list[0]\n}\n" +
				"output \"j\" {\n  value = var.list[*].id\n}\n" +
				"output \"k\" {\n  value = \"${var.a}-${var.b}\"\n}\n"},
			want: []string{
				"structure.output-no-try outputs.tf:16 .:output.e",
				"structure.output-no-try outputs.tf:19 .:output.f",
				"structure.output-no-try outputs.tf:22 .:output.g",
				"structure.output-no-try outputs.tf:25 .:output.h",
				"structure.output-no-try outputs.tf:28 .:output.i",
				"structure.output-no-try outputs.tf:31 .:output.j",
			},
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
			name: "wrong file: locals group, and a feature file makes main.tf resources candidates",
			head: map[string]string{
				"main.tf":       "resource \"aws_kms_key\" \"a\" {}\nlocals {\n  x = 1\n}\ndata \"aws_demo\" \"d\" {}\n",
				"encryption.tf": "",
				"locals.tf":     "locals {\n  y = 2\n}\n",
				"other.tf":      "resource \"aws_kms_key\" \"b\" {}\n",
			},
			want: []string{"structure.wrong-file main.tf:1 .:aws_kms_key.a", "structure.wrong-file main.tf:3 .:local.x", "structure.wrong-file main.tf:5 .:data.aws_demo.d"},
		},
		{
			name: "wrong file: an identity resource with iam.tf is the iam.tf candidate only; iam.tf as a directory is not the file",
			head: map[string]string{
				"main.tf":                "resource \"aws_iam_role\" \"a\" {}\n",
				"iam.tf":                 "",
				"logging.tf":             "",
				"modules/m/main.tf":      "resource \"aws_iam_role\" \"c\" {}\n",
				"modules/m/iam.tf/x.txt": "",
			},
			want: []string{"structure.wrong-file main.tf:1 .:aws_iam_role.a"},
		},
		{
			name: "multi-provider: ephemeral, import and a data block in a check block",
			head: map[string]string{"main.tf": "ephemeral \"aws_demo\" \"e\" {\n  provider = aws.x\n}\n" +
				"import {\n  to       = aws_demo_widget.this\n  id       = \"x\"\n  provider = aws.x\n}\n" +
				"check \"c\" {\n  data \"aws_demo\" \"a\" {}\n  data \"aws_demo\" \"b\" {\n    provider = aws.x\n  }\n  assert {\n    condition     = true\n    error_message = \"x\"\n  }\n}\n"},
			want: []string{
				"structure.multi-provider main.tf:12 .:check.c.data[1].provider",
				"structure.multi-provider main.tf:2 .:ephemeral.aws_demo.e.provider",
				"structure.multi-provider main.tf:7 .:import.provider",
			},
		},
		{
			name: "arn: format verbs, a heredoc line, terraform and removed blocks",
			head: map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n" +
				"  a = format(\"arn:%s:iam::%s:role/x\", local.p, local.a)\n" +
				"  b = <<-EOT\n    first line\n    arn:${local.p}:iam::123456789012:role/x\n  EOT\n}\n" +
				"terraform {\n  backend \"s3\" {\n    role_arn = \"arn:aws:iam::123456789012:role/state\"\n  }\n}\n" +
				"removed {\n  from = aws_demo_widget.old\n  lifecycle {\n    destroy = false\n  }\n  provisioner \"local-exec\" {\n    command = \"echo arn:aws:s3:::x\"\n  }\n}\n"},
			want: []string{
				"structure.arn-partition-hardcoded main.tf:10 .:backend",
				"structure.arn-partition-hardcoded main.tf:19 .:removed.aws_demo_widget.old",
				"structure.arn-partition-hardcoded main.tf:2 .:aws_demo_widget.this.a",
				"structure.arn-partition-hardcoded main.tf:5 .:aws_demo_widget.this.b",
			},
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

func TestIdentityTypesNamedByTheArchetypes(t *testing.T) {
	for _, typ := range []string{"aws_iam_role", "aws_iam_policy_document", "google_service_account",
		"google_project_iam_member", "azurerm_role_assignment", "azurerm_user_assigned_identity"} {
		if !identityType("resource", typ) {
			t.Errorf("%s is not an identity type", typ)
		}
	}
	for _, typ := range []string{"aws_kms_key", "aws_cloudwatch_log_group", "google_storage_bucket", "azurerm_resource_group"} {
		if identityType("resource", typ) {
			t.Errorf("%s is an identity type", typ)
		}
	}
}

// Nine misplaced tags blocks are past the source line cap.
func TestStructureLocalSourceLineCap(t *testing.T) {
	body := strings.Repeat("  tags {}\n  name = 1\n", 1) + strings.Repeat("  tags {}\n", 8) + "  name2 = 1\n"
	r, err := Run(Options{Head: writeTree(t, map[string]string{
		"main.tf": "resource \"aws_demo_widget\" \"this\" {\n" + body + "}\n",
	}), Limits: DefaultLimits, ProfileDir: testProfile})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(r.Candidates, func(c Candidate) bool { return c.RuleID == "structure.argument-order" })
	if i < 0 {
		t.Fatalf("no candidate: %+v", r.Candidates)
	}
	if c := r.Candidates[i]; c.EvidenceComplete || len(c.Evidence.SourceLines) != maxEvidenceComments || len(*c.Evidence.Head) >= maxEvidenceText {
		t.Errorf("line cap did not bite: complete=%v lines=%d", c.EvidenceComplete, len(c.Evidence.SourceLines))
	}
}

func TestReparseFailureIsACandidate(t *testing.T) {
	l := Local{Name: "x", Value: Expr{Text: "(", Range: hcl.Range{Start: hcl.Pos{Line: 3, Column: 7}}}}
	l.File, l.Line = "main.tf", 3
	a := &Area{Path: ".", Kind: AreaRoot, headTree: &Tree{Entries: map[string]*Entry{}}, Head: &Module{Locals: []Local{l}}}
	res := checkARNHardcoded(a)
	if len(res.Candidates) != 1 || res.Candidates[0].Subject != ".:local.x" || *res.Candidates[0].Line != 3 {
		t.Errorf("candidates %+v", res.Candidates)
	}
	if outputSafe("outputs.tf", Expr{Text: "("}) {
		t.Error("an output that does not parse again is safe")
	}
}
