package facts

import "testing"

// One case per survivor of the mutation run in the trace and the key
// closure that a test can reach. The cases marked "equal" are those where
// the weakened code compares two different trees equal.
func TestPolicyDocsFlowGuards(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
	const admin = `data "aws_iam_policy_document" "admin" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
`
	const xAddr = "data.aws_iam_policy_document.x"
	run := func(t *testing.T, head, base map[string]string) *PolicyDocsReport {
		t.Helper()
		o := PolicyDocsOptions{Head: writeTree(t, head), Limits: DefaultLimits}
		if base != nil {
			o.Base = writeTree(t, base)
		}
		r, err := PolicyDocs(o)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	child := "variable \"p\" {}\noutput \"s\" {\n  value = var.p\n}\n"
	call := "module \"m\" {\n  source   = \"./m\"\n  for_each = toset([\"a\"])\n  p        = data.aws_iam_policy_document.x.json\n}\n"
	undecided := map[string]map[string]string{
		"a function of a variable in the child": {"main.tf": doc + "module \"m\" {\n  source = \"./m\"\n  p      = data.aws_iam_policy_document.x.json\n}\n",
			"m/main.tf": "variable \"p\" {}\nresource \"aws_iam_policy\" \"p\" {\n  policy = upper(var.p)\n}\n"},
		"two keys on a module call":       {"main.tf": doc + call + "resource \"aws_iam_policy\" \"p\" {\n  policy = module.m[\"a\"][\"s\"]\n}\n", "m/main.tf": child},
		"a key on a document's json":      {"main.tf": doc + "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json[0]\n}\n"},
		"a key on a module output":        {"main.tf": doc + "module \"m\" {\n  source = \"./m\"\n  p      = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = module.m.s[0]\n}\n", "m/main.tf": child},
		"another attribute of a document": {"main.tf": doc + "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.id\n}\n"},
		"an attribute of a string local":  {"main.tf": doc + "locals {\n  l = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = local.l.foo\n}\n"},
	}
	for name, files := range undecided {
		t.Run(name, func(t *testing.T) {
			if d := policyDoc(t, run(t, files, nil).Head, ".", xAddr); d.Uses != nil || d.UsesUndecidable == nil {
				t.Errorf("uses: %s", canonJSON(d.Uses))
			}
		})
	}
	t.Run("a whole resource object as a policy is a document of its own", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "resource \"aws_s3_object\" \"o\" {\n  bucket = \"b\"\n  key    = \"k\"\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = aws_s3_object.o\n}\n"}, nil)
		if d := policyDoc(t, r.Head, ".", "aws_iam_policy.p.policy"); d.Kind != PolicyKindExpression {
			t.Errorf("document: %+v", d)
		}
	})
	t.Run("a local named like the data type reads no document", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc + "locals {\n  aws_iam_policy_document = {x = {json = \"{}\"}}\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = local.aws_iam_policy_document.x.json\n}\n"}, nil)
		if d := policyDoc(t, r.Head, ".", xAddr); d.Uses == nil || len(d.Uses) != 0 {
			t.Errorf("uses: %s %+v", canonJSON(d.Uses), d.UsesUndecidable)
		}
	})
	t.Run("blocks without labels that hold arguments", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc + "output {\n  value = 1\n}\nmodule {\n  source = \"./m\"\n  p      = 1\n}\ndata \"aws_iam_policy_document\" {\n  statement {\n    actions = [\"*\"]\n  }\n}\n",
			"m/main.tf": "variable \"p\" {}\n"}, nil)
		if len(r.Head.Documents) != 1 {
			t.Errorf("documents: %d", len(r.Head.Documents))
		}
	})
	t.Run("equal: content reads count.index and the gate reads a file", func(t *testing.T) {
		files := map[string]string{"main.tf": "data \"aws_iam_policy_document\" \"x\" {\n  count = length(file(\"f.txt\"))\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [\"arn:aws:s3:::b${count.index}\"]\n  }\n}\n", "f.txt": "a\n"}
		p := policyPair(t, run(t, files, files), ".", xAddr)
		if p.Content == nil || *p.Content != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("an inline document in a local is named by the local", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "locals {\n  l = jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"s3:GetObject\", Resource = \"*\"}]})\n}\n"}, nil)
		policyDoc(t, r.Head, ".", "local.l")
	})
	t.Run("equal: a local after an inline document gains a document", func(t *testing.T) {
		tree := func(extra string) map[string]string {
			return map[string]string{"main.tf": admin + `locals {
  l     = jsonencode({Version = "2012-10-17", Statement = [{Effect = "Allow", Action = "s3:GetObject", Resource = "*"}]})
  extra = ` + extra + `
  m     = concat([local.l], local.extra)
}
data "aws_iam_policy_document" "merged" {
  source_policy_documents = local.m
}
resource "aws_iam_role_policy" "p" {
  role   = "r"
  policy = data.aws_iam_policy_document.merged.json
}
`}
		}
		p := policyPair(t, run(t, tree("[data.aws_iam_policy_document.admin.json]"), tree("[]")), ".", "local.l")
		if p.Uses == nil || *p.Uses != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("equal: an output of an inline document becomes a list", func(t *testing.T) {
		const l = "locals {\n  l = jsonencode({Version = \"2012-10-17\", Statement = [{Effect = \"Allow\", Action = \"s3:GetObject\", Resource = \"*\"}]})\n}\n"
		p := policyPair(t, run(t, map[string]string{"main.tf": l + "output \"o\" {\n  value = [local.l]\n}\n"},
			map[string]string{"main.tf": l + "output \"o\" {\n  value = local.l\n}\n"}), ".", "local.l")
		if p.Uses == nil || *p.Uses != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("equal: an output of a document becomes a list", func(t *testing.T) {
		p := policyPair(t, run(t, map[string]string{"main.tf": doc + "output \"o\" {\n  value = [data.aws_iam_policy_document.x.json]\n}\n"},
			map[string]string{"main.tf": doc + "output \"o\" {\n  value = data.aws_iam_policy_document.x.json\n}\n"}), ".", xAddr)
		if p.Uses == nil || *p.Uses != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
	same := func(t *testing.T, files map[string]string) PolicyPair {
		t.Helper()
		return policyPair(t, run(t, files, files), ".", xAddr)
	}
	t.Run("equal: a document gate reads a file", func(t *testing.T) {
		p := same(t, map[string]string{"main.tf": "data \"aws_iam_policy_document\" \"x\" {\n  count = length(file(\"f.txt\")) > 0 ? 1 : 0\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [\"*\"]\n  }\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[0].json\n}\n", "f.txt": "a\n"})
		if p.Uses == nil || *p.Uses != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("equal: a provider reads a type outside the provider", func(t *testing.T) {
		if p := same(t, map[string]string{"main.tf": doc + "resource \"random_pet\" \"r\" {}\nprovider \"aws\" {\n  region = random_pet.r.id\n}\n"}); p.Presence != PresenceUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("equal: a caller's call block reads a file", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "module \"m\" {\n  source = \"./m\"\n  count  = length(file(\"f.txt\"))\n}\n", "f.txt": "a\n",
			"m/main.tf": doc + "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json\n}\n"},
			map[string]string{"main.tf": "module \"m\" {\n  source = \"./m\"\n  count  = length(file(\"f.txt\"))\n}\n", "f.txt": "",
				"m/main.tf": doc + "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json\n}\n"})
		if p := policyPair(t, r, "m", xAddr); p.Uses == nil || *p.Uses != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("equal: content reads a data source outside the provider", func(t *testing.T) {
		p := same(t, map[string]string{"main.tf": "data \"external\" \"e\" {\n  program = [\"sh\", \"run.sh\"]\n}\ndata \"aws_iam_policy_document\" \"x\" {\n  statement {\n    actions   = [\"s3:GetObject\"]\n    resources = [\"${data.external.e.result.arn}\"]\n  }\n}\n", "run.sh": "echo\n"})
		if p.Content == nil || *p.Content != CompareUndecidable {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("a use under a call while a directory is undecidable", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "module \"m\" {\n  source = \"./m\"\n}\n", "bad/x.tf.json": "{}\n",
			"m/main.tf": doc + "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json\n}\n"}, nil)
		if d := policyDoc(t, r.Head, "m", xAddr); len(d.Uses) != 1 || d.Uses[0].Instances != InstancesConditional {
			t.Errorf("uses: %s", canonJSON(d.Uses))
		}
	})
}
