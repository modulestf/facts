package facts

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// affectedHead is a module with a root, a submodule and examples that call
// them by local path, transitively, and by registry and git source.
var affectedHead = map[string]string{
	"main.tf":                      "resource \"null_resource\" \"this\" {}\n",
	"README.md":                    "# module\n",
	".github/workflows/ci.yml":     "on: push\n",
	"modules/sub/main.tf":          "resource \"null_resource\" \"sub\" {}\n",
	"modules/sub/README.md":        "# sub\n",
	"modules/sub/files/run.py":     "print(1)\n",
	"examples/complete/main.tf":    "module \"this\" {\n  source = \"../../\"\n}\n",
	"examples/complete/README.md":  "# complete\n",
	"examples/sub/main.tf":         "module \"sub\" {\n  source = \"../../modules/sub\"\n}\n",
	"examples/chain/main.tf":       "module \"local\" {\n  source = \"./local\"\n}\n",
	"examples/chain/local/main.tf": "module \"root\" {\n  source = \"../../../\"\n}\n",
	"examples/registry/main.tf": "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"6.0.0\"\n}\n" +
		"module \"git\" {\n  source = \"git::https://example.com/m.git?ref=v1\"\n}\n",
}

// with returns a copy of files with the given paths set, or removed when the
// value is "".
func with(files map[string]string, set map[string]string) map[string]string {
	out := maps.Clone(files)
	for p, v := range set {
		if v == "" {
			delete(out, p)
		} else {
			out[p] = v
		}
	}
	return out
}

func runAffected(t *testing.T, head, base map[string]string) *AffectedReport {
	t.Helper()
	o := AffectedOptions{Head: writeTree(t, head), Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	r, err := Affected(o)
	if err != nil {
		t.Fatal(err)
	}
	checkAffectedSchema(t, r)
	return r
}

func checkAffectedSchema(t *testing.T, r *AffectedReport) {
	t.Helper()
	if errs := loadSchemas(t).validate("affected.schema.json", decodeJSON(t, mustJSON(t, r))); len(errs) > 0 {
		t.Errorf("report does not match affected.schema.json: %v", errs)
	}
}

// pairs renders the examples as "reason dir" in order.
func pairs(r *AffectedReport) []string {
	var out []string
	for _, e := range r.Examples {
		out = append(out, e.Reason+" "+e.Dir)
	}
	return out
}

func wantAffected(t *testing.T, r *AffectedReport, inert bool, fallback string, examples ...string) {
	t.Helper()
	if r.Inert != inert {
		t.Errorf("inert = %v, want %v", r.Inert, inert)
	}
	got := ""
	if r.Fallback != nil {
		got = *r.Fallback
	}
	if got != fallback {
		t.Errorf("fallback = %q, want %q", got, fallback)
	}
	if !slices.Equal(pairs(r), examples) {
		t.Errorf("examples = %q, want %q", pairs(r), examples)
	}
}

func TestAffectedSelection(t *testing.T) {
	cases := map[string]struct {
		set      map[string]string // head changes against affectedHead as base
		inert    bool
		examples []string
	}{
		"touched example": {
			set:      map[string]string{"examples/sub/main.tf": affectedHead["examples/sub/main.tf"] + "# edit\n"},
			examples: []string{"touched examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"root change reaches callers transitively, registry and git are not callers": {
			set:      map[string]string{"main.tf": "resource \"null_resource\" \"this\" {\n  triggers = {}\n}\n"},
			examples: []string{"caller examples/chain", "caller examples/complete", "all examples/registry", "all examples/sub"},
		},
		"submodule change": {
			set:      map[string]string{"modules/sub/main.tf": "resource \"null_resource\" \"sub2\" {}\n"},
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"nested local module of an example": {
			set:      map[string]string{"examples/chain/local/main.tf": "module \"root\" {\n  source = \"../../..\"\n}\n"},
			examples: []string{"touched examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"},
		},
		"non-config file goes to the nearest module directory": {
			set:      map[string]string{"modules/sub/files/run.py": "print(2)\n"},
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"docs only is inert": {
			set:      map[string]string{"README.md": "# changed\n", "modules/sub/README.md": "# changed\n", ".github/workflows/ci.yml": "on: pull_request\n"},
			inert:    true,
			examples: []string{"all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"},
		},
		"docs in an example is inert but touched": {
			set:      map[string]string{"examples/complete/README.md": "# changed\n"},
			inert:    true,
			examples: []string{"touched examples/complete", "all examples/chain", "all examples/registry", "all examples/sub"},
		},
		"removed submodule file": {
			set:      map[string]string{"modules/sub/README.md": "", "modules/sub/main.tf": "resource \"null_resource\" \"sub\" {}\n# x\n"},
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"lock file is not inert": {
			set:      map[string]string{".terraform.lock.hcl": "# lock\n"},
			examples: []string{"caller examples/chain", "caller examples/complete", "all examples/registry", "all examples/sub"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := runAffected(t, with(affectedHead, tc.set), affectedHead)
			wantAffected(t, r, tc.inert, "", tc.examples...)
			var want []string
			for p := range tc.set {
				want = append(want, p)
			}
			slices.Sort(want)
			if !slices.Equal(r.Changed, want) {
				t.Errorf("changed = %q, want %q", r.Changed, want)
			}
		})
	}
}

// TestAffectedReadSites checks that a docs file some HCL read site reaches
// is not inert, and counts as a change of the module that reads it.
func TestAffectedReadSites(t *testing.T) {
	readme := map[string]string{"README.md": "# changed\n"}
	cases := map[string]struct {
		hcl      map[string]string // set in both trees
		change   map[string]string
		inert    bool
		examples []string
	}{
		"file of path.module": {
			hcl:      map[string]string{"main.tf": "locals {\n  doc = file(\"${path.module}/README.md\")\n}\n"},
			change:   readme,
			examples: []string{"caller examples/chain", "caller examples/complete", "all examples/registry", "all examples/sub"},
		},
		"templatefile with a dynamic path reaches everything": {
			hcl:      map[string]string{"modules/sub/main.tf": "locals {\n  t = templatefile(var.tpl, {})\n}\n"},
			change:   readme,
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"yamldecode of file under a dynamic tail": {
			hcl:      map[string]string{"modules/sub/main.tf": "locals {\n  y = yamldecode(file(\"${path.module}/${var.name}.md\"))\n}\n"},
			change:   map[string]string{"modules/sub/README.md": "# changed\n"},
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"dynamic tail under another directory does not reach": {
			hcl:      map[string]string{"modules/sub/main.tf": "locals {\n  y = file(\"${path.module}/files/${var.name}\")\n}\n"},
			change:   map[string]string{"modules/sub/README.md": "# changed\n"},
			inert:    true,
			examples: []string{"all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"},
		},
		"path.module outside a read function": {
			hcl:      map[string]string{"main.tf": "resource \"aws_s3_object\" \"this\" {\n  source = \"${path.module}/README.md\"\n}\n"},
			change:   readme,
			examples: []string{"caller examples/chain", "caller examples/complete", "all examples/registry", "all examples/sub"},
		},
		"relative literal resolves against the root module": {
			hcl:      map[string]string{"examples/complete/main.tf": "module \"this\" {\n  source = \"../../\"\n  doc    = \"README.md\"\n}\n"},
			change:   map[string]string{"examples/complete/README.md": "# changed\n"},
			examples: []string{"touched examples/complete", "all examples/chain", "all examples/registry", "all examples/sub"},
		},
		"local_file filename": {
			hcl:      map[string]string{"modules/sub/main.tf": "data \"local_file\" \"d\" {\n  filename = var.f\n}\n"},
			change:   readme,
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"local-exec command": {
			hcl:      map[string]string{"modules/sub/main.tf": "resource \"null_resource\" \"x\" {\n  provisioner \"local-exec\" {\n    command = local.cmd\n  }\n}\n"},
			change:   readme,
			examples: []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"},
		},
		"unrelated read site": {
			hcl:      map[string]string{"modules/sub/main.tf": "locals {\n  p = file(\"${path.module}/policy.json\")\n}\n"},
			change:   readme,
			inert:    true,
			examples: []string{"all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			base := with(affectedHead, tc.hcl)
			r := runAffected(t, with(base, tc.change), base)
			wantAffected(t, r, tc.inert, "", tc.examples...)
		})
	}
}

func TestAffectedFallback(t *testing.T) {
	all := []string{"all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"}
	cases := map[string]struct {
		hcl      map[string]string
		fallback string
	}{
		"parse error":           {map[string]string{"modules/sub/main.tf": "resource \"x\" {\n"}, FallbackUnparsed},
		"json configuration":    {map[string]string{"modules/sub/extra.tf.json": "{}\n"}, FallbackUnparsed},
		"dynamic module source": {map[string]string{"examples/sub/main.tf": "module \"sub\" {\n  source = \"../../${var.m}\"\n}\n"}, FallbackDynamicSource},
		"module with no source": {map[string]string{"examples/sub/main.tf": "module \"sub\" {\n}\n"}, FallbackDynamicSource},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			base := with(affectedHead, tc.hcl)
			r := runAffected(t, with(base, map[string]string{"README.md": "# changed\n"}), base)
			wantAffected(t, r, false, tc.fallback, all...)
		})
	}
	t.Run("no change input", func(t *testing.T) {
		r := runAffected(t, affectedHead, nil)
		wantAffected(t, r, false, FallbackNoChangeInput, all...)
		if len(r.Changed) != 0 {
			t.Errorf("changed = %q, want none", r.Changed)
		}
	})
}

func TestAffectedNoChange(t *testing.T) {
	r := runAffected(t, affectedHead, affectedHead)
	wantAffected(t, r, true, "", "all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub")
}

func TestAffectedExampleDirs(t *testing.T) {
	t.Run("only first level dirs holding a .tf file", func(t *testing.T) {
		head := with(affectedHead, map[string]string{"examples/notes/README.md": "x\n", "examples/README.md": "x\n"})
		r := runAffected(t, head, head)
		if got := len(r.Examples); got != 4 {
			t.Errorf("examples = %q, want the 4 with a .tf file", pairs(r))
		}
	})
	t.Run("a removed example is not listed", func(t *testing.T) {
		head := with(affectedHead, map[string]string{"examples/sub/main.tf": ""})
		r := runAffected(t, head, affectedHead)
		wantAffected(t, r, false, "", "all examples/chain", "all examples/complete", "all examples/registry")
	})
	t.Run("an unsafe example name is an error", func(t *testing.T) {
		head := with(affectedHead, map[string]string{"examples/bad name/main.tf": "locals {}\n"})
		_, err := Affected(AffectedOptions{Head: writeTree(t, head), Base: writeTree(t, head), Limits: DefaultLimits})
		if err == nil || !strings.Contains(err.Error(), "example") {
			t.Errorf("err = %v, want an example name error", err)
		}
	})
}

// TestAffectedDiff gives the change as a GitHub files list and as a unified
// diff: both rebuild the base and select as --base does.
func TestAffectedDiff(t *testing.T) {
	head := with(affectedHead, map[string]string{"modules/sub/main.tf": "resource \"null_resource\" \"sub2\" {}\n"})
	want := []string{"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry"}
	diffs := map[string]string{
		"github files list": `[{"filename":"modules/sub/main.tf","status":"modified","additions":1,"deletions":1,` +
			`"patch":"@@ -1 +1 @@\n-resource \"null_resource\" \"sub\" {}\n+resource \"null_resource\" \"sub2\" {}"}]`,
		"unified diff": "diff --git a/modules/sub/main.tf b/modules/sub/main.tf\n--- a/modules/sub/main.tf\n+++ b/modules/sub/main.tf\n" +
			"@@ -1 +1 @@\n-resource \"null_resource\" \"sub\" {}\n+resource \"null_resource\" \"sub2\" {}\n",
	}
	for name, d := range diffs {
		t.Run(name, func(t *testing.T) {
			f := filepath.Join(t.TempDir(), "change")
			if err := os.WriteFile(f, []byte(d), 0o600); err != nil {
				t.Fatal(err)
			}
			r, err := Affected(AffectedOptions{Head: writeTree(t, head), Diff: f, Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			checkAffectedSchema(t, r)
			wantAffected(t, r, false, "", want...)
			if !slices.Equal(r.Changed, []string{"modules/sub/main.tf"}) {
				t.Errorf("changed = %q", r.Changed)
			}
		})
	}
	t.Run("a diff that does not fit is an error", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "change")
		if err := os.WriteFile(f, []byte(`[{"filename":"nope.tf","status":"modified","patch":"@@ -1 +1 @@\n-a\n+b"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Affected(AffectedOptions{Head: writeTree(t, head), Diff: f, Limits: DefaultLimits}); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("base and diff together is an error", func(t *testing.T) {
		if _, err := Affected(AffectedOptions{Head: writeTree(t, head), Base: writeTree(t, head), Diff: "x", Limits: DefaultLimits}); err == nil {
			t.Error("want an error")
		}
	})
}

func TestInertDocPath(t *testing.T) {
	for p, want := range map[string]bool{
		"README.md": true, "modules/a/README.md": true, "docs/x.png": true, "docs/guide.rst": true,
		".github/workflows/ci.yml": true, "LICENSE": true, "CHANGELOG.md": true, ".gitignore": true,
		".pre-commit-config.yaml": true, ".editorconfig": true,
		"main.tf": false, "docs/example.tf": false, "x.tf.json": false, "terraform.tfvars": false,
		".terraform.lock.hcl": false, "tests/a.tftest.hcl": false, "policy.json": false, "examples/a/diagram.png": false,
		"scripts/run.sh": false, "templates/user_data.tpl": false,
	} {
		if got := InertDocPath(p); got != want {
			t.Errorf("InertDocPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestAffectedReadSiteCap treats a tree with more read sites than the cap as
// one that does not parse, so no docs change is inert there.
func TestAffectedReadSiteCap(t *testing.T) {
	old := maxReadSites
	maxReadSites = 1
	t.Cleanup(func() { maxReadSites = old })
	base := with(affectedHead, map[string]string{"main.tf": "locals {\n  a = \"x.json\"\n  b = \"y.json\"\n}\n"})
	r := runAffected(t, with(base, map[string]string{"README.md": "# changed\n"}), base)
	wantAffected(t, r, false, FallbackUnparsed, "all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub")
}
