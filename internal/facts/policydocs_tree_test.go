package facts

import (
	"path/filepath"
	"testing"
)

// One case per guard of the tree reading and the comparison precondition
// that no other test reaches.
func TestPolicyDocsTreeGuards(t *testing.T) {
	const doc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
`
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
	undecided := func(t *testing.T, head, base map[string]string) {
		t.Helper()
		if p := policyPair(t, run(t, head, base), ".", "data.aws_iam_policy_document.x"); p.Presence != PresenceUndecidable {
			t.Errorf("pair: %+v", p)
		}
	}
	dirReason := func(t *testing.T, r *PolicyDocsReport, dir string) *PolicyReason {
		t.Helper()
		for _, d := range r.Head.Directories {
			if d.Dir == dir {
				return d.Undecidable
			}
		}
		t.Fatalf("no directory %q", dir)
		return nil
	}
	plain := map[string]string{"main.tf": doc}
	t.Run("the base alone fails the precondition", func(t *testing.T) {
		undecided(t, plain, map[string]string{"main.tf": doc, "terraform.tfvars": "a = 1\n"})
	})
	t.Run("a JSON variables file", func(t *testing.T) {
		undecided(t, map[string]string{"main.tf": doc, "terraform.tfvars.json": "{}\n"}, plain)
	})
	t.Run("a terraform block differs", func(t *testing.T) {
		undecided(t, map[string]string{"main.tf": doc + "terraform {\n  required_version = \">= 1.5\"\n}\n"},
			map[string]string{"main.tf": doc + "terraform {\n  required_version = \">= 1.0\"\n}\n"})
	})
	for name, src := range map[string]string{
		"an absolute source":     `"/opt/m"`,
		"the directory itself":   `"."`,
		"the parent":             `".."`,
		"a source not a literal": `var.s`,
		"no source":              ``,
	} {
		t.Run("a module call with "+name, func(t *testing.T) {
			call := "variable \"s\" {\n  type = string\n}\nmodule \"m\" {\n"
			if src != "" {
				call += "  source = " + src + "\n"
			}
			call += "}\n"
			files := map[string]string{"main.tf": doc + call}
			undecided(t, files, files)
		})
	}
	t.Run("a registry module call keeps the comparison", func(t *testing.T) {
		files := map[string]string{"main.tf": doc + "module \"m\" {\n  source = \"terraform-aws-modules/iam/aws\"\n}\n"}
		if p := policyPair(t, run(t, files, files), ".", "data.aws_iam_policy_document.x"); p.Presence != PresenceBoth || *p.Content != CompareEqual {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("blocks without their labels", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc + "variable {\n}\noutput {\n}\nmodule {\n}\nresource \"aws_iam_policy\" {\n}\ndata \"x\" {\n}\n"}, nil)
		if len(r.Head.Documents) != 1 {
			t.Errorf("documents: %d", len(r.Head.Documents))
		}
	})
	t.Run("an OpenTofu JSON file", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc, "extra.tofu.json": "{}\n"}, nil)
		if dirReason(t, r, ".") == nil {
			t.Error("directory decided")
		}
	})
	t.Run("a linked configuration file", func(t *testing.T) {
		target := filepath.Join(writeTree(t, map[string]string{"real.tf": doc}), "real.tf")
		r := run(t, map[string]string{"main.tf": doc, "link.tf": "->" + target}, nil)
		if dirReason(t, r, ".") == nil {
			t.Error("directory decided")
		}
	})
	t.Run("a directory with only a hidden file", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": doc, "h/.x.tf": doc}, nil)
		for _, d := range r.Head.Directories {
			if d.Dir == "h" {
				t.Error("a directory Terraform loads nothing from is listed")
			}
		}
	})
	t.Run("a base directory that does not exist", func(t *testing.T) {
		_, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, plain), Base: filepath.Join(t.TempDir(), "none"), Limits: DefaultLimits})
		if err == nil {
			t.Error("no error")
		}
	})
}
