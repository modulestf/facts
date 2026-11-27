package facts

import "testing"

// One case per guard of the use trace and the sink scan that no other test
// reaches.
func TestPolicyDocsTraceGuards(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
	const xAddr = "data.aws_iam_policy_document.x"
	run := func(t *testing.T, files map[string]string) *PolicyDocsReport {
		t.Helper()
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, files), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range r.Head.Directories {
			if d.Undecidable != nil {
				t.Fatalf("directory %s undecidable, the case tests nothing: %+v", d.Dir, d.Undecidable)
			}
		}
		return r
	}
	has := func(r *PolicyDocsReport, dir, addr string) bool {
		for _, d := range r.Head.Documents {
			if d.Dir == dir && d.Address == addr {
				return true
			}
		}
		return false
	}
	child := "variable \"p\" {}\noutput \"o\" {\n  value = [var.p]\n}\noutput \"s\" {\n  value = var.p\n}\n"
	// The use of x must be undecidable: the reader cannot follow the value.
	undecided := map[string]string{
		"a function of a local":           "locals {\n  l = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = upper(local.l)\n}\n",
		"a function of a module output":   "module \"m\" {\n  source = \"./m\"\n  p      = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = upper(module.m.s)\n}\n",
		"a function of the module object": "module \"m\" {\n  source = \"./m\"\n  p      = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = jsonencode(module.m)\n}\n",
		"an index into a module output":   "module \"m\" {\n  source = \"./m\"\n  p      = data.aws_iam_policy_document.x.json\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = module.m.o[0]\n}\n",
		"a null instance key":             "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[null].json\n}\n",
		"two instance keys":               "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[0][1].json\n}\n",
		"an attribute of the json":        "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x.json.y\n}\n",
		"the whole document object":       "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x\n}\n",
		"an attribute of a local":         "locals {\n  l = {a = data.aws_iam_policy_document.x.json}\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = local.l.a\n}\n",
		"an expanded argument":            "locals {\n  l = [[data.aws_iam_policy_document.x.json]]\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = concat(local.l...)[0]\n}\n",
		"flatten with two arguments":      "resource \"aws_iam_role_policy\" \"p\" {\n  role   = \"r\"\n  policy = flatten([data.aws_iam_policy_document.x.json], [])\n}\n",
		"a splat with no attribute":       "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[*]\n}\n",
		"a splat of another attribute":    "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[*].id\n}\n",
		"a splat with two steps":          "resource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_iam_policy_document.x[*].json.y\n}\n",
		"a splat of a local":              "locals {\n  l = [data.aws_iam_policy_document.x]\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = local.l[*].json\n}\n",
	}
	for name, use := range undecided {
		t.Run(name, func(t *testing.T) {
			r := run(t, map[string]string{"main.tf": doc + use, "m/main.tf": child})
			if d := policyDoc(t, r.Head, ".", xAddr); d.Uses != nil || d.UsesUndecidable == nil {
				t.Errorf("uses: %s", canonJSON(d.Uses))
			}
		})
	}
	// The use of x is the one resource, traced.
	traced := map[string]string{
		"through compact":           "data \"aws_iam_policy_document\" \"m\" {\n  source_policy_documents = compact([data.aws_iam_policy_document.x.json])\n}\nresource \"aws_iam_role_policy\" \"p\" {\n  role   = \"r\"\n  policy = data.aws_iam_policy_document.m.json\n}\n",
		"beside a depends_on":       "resource \"aws_iam_role_policy\" \"p\" {\n  role       = \"r\"\n  policy     = data.aws_iam_policy_document.x.json\n  depends_on = [data.aws_iam_policy_document.x]\n}\n",
		"beside a lifecycle":        "resource \"aws_iam_role_policy\" \"p\" {\n  role   = \"r\"\n  policy = data.aws_iam_policy_document.x.json\n  lifecycle {\n    replace_triggered_by = [data.aws_iam_policy_document.x]\n  }\n}\n",
		"in a labeled nested block": "resource \"aws_iam_role_policy\" \"p\" {\n  role = \"r\"\n  provisioner \"local-exec\" {\n    command = data.aws_iam_policy_document.x.json\n  }\n}\n",
	}
	for name, use := range traced {
		t.Run(name, func(t *testing.T) {
			r := run(t, map[string]string{"main.tf": doc + use})
			if d := policyDoc(t, r.Head, ".", xAddr); len(d.Uses) != 1 || d.Uses[0].Address != "aws_iam_role_policy.p" {
				t.Errorf("uses: %s %+v", canonJSON(d.Uses), d.UsesUndecidable)
			}
		})
	}
	// A policy argument whose value no document explains is a document of
	// its own; one that is null, or explained, is not.
	sinks := map[string]struct {
		file, addr string
		want       bool
	}{
		"a splat of another data source":    {"data \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = data.aws_s3_bucket.b[*].json\n}\n", "aws_iam_policy.p.policy", true},
		"a name ending in policy_document":  {"variable \"v\" {}\nresource \"aws_x\" \"y\" {\n  source_policy_document = var.v\n}\n", "aws_x.y.source_policy_document", true},
		"a data source argument":            {"variable \"v\" {}\ndata \"aws_x\" \"y\" {\n  policy = var.v\n}\n", "data.aws_x.y.policy", true},
		"a provider argument":               {"variable \"v\" {}\nprovider \"aws\" {\n  assume_role {\n    policy = var.v\n  }\n}\n", "provider.aws.assume_role[0].policy", true},
		"a null policy":                     {"resource \"aws_iam_policy\" \"p\" {\n  policy = null\n}\n", "aws_iam_policy.p.policy", false},
		"a jsonencode with no Statement":    {"resource \"aws_iam_policy\" \"p\" {\n  policy = jsonencode({Version = \"2012-10-17\"})\n}\n", "aws_iam_policy.p.policy", true},
		"a local other than the first one":  {doc + "variable \"v\" {}\nlocals {\n  a = data.aws_iam_policy_document.x.json\n  b = var.v\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = local.b\n}\n", "aws_iam_policy.p.policy", true},
		"a resource of the document's type": {"variable \"v\" {}\nresource \"aws_iam_policy_document\" \"z\" {\n  statement = var.v\n}\nresource \"aws_iam_policy\" \"p\" {\n  policy = aws_iam_policy_document.z.json\n}\n", "aws_iam_policy.p.policy", true},
	}
	for name, c := range sinks {
		t.Run(name, func(t *testing.T) {
			if got := has(run(t, map[string]string{"main.tf": c.file}), ".", c.addr); got != c.want {
				t.Errorf("document %s: %v", c.addr, got)
			}
		})
	}
	t.Run("a resource of the document's type is no document", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": "resource \"aws_iam_policy_document\" \"z\" {\n}\n"})
		if has(r, ".", "aws_iam_policy_document.z") || has(r, ".", xAddr) {
			t.Errorf("documents: %+v", r.Head.Documents)
		}
	})
	for gate, want := range map[string]string{
		"[]":       InstancesNone,
		"{}":       InstancesNone,
		"[\"a\"]":  InstancesAlways,
		"{a = 1}":  InstancesAlways,
		"var.keys": InstancesConditional,
	} {
		t.Run("for_each = "+gate, func(t *testing.T) {
			r := run(t, map[string]string{"main.tf": doc + "variable \"keys\" {}\nresource \"aws_iam_role_policy\" \"p\" {\n  for_each = " + gate + "\n  role     = \"r\"\n  policy   = data.aws_iam_policy_document.x.json\n}\n"})
			if d := policyDoc(t, r.Head, ".", xAddr); len(d.Uses) != 1 || d.Uses[0].Instances != want {
				t.Errorf("uses: %s", canonJSON(d.Uses))
			}
		})
	}
}
