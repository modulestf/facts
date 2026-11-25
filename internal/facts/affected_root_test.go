package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestAffectedModuleRootFixture runs testdata/affected/module-root, the
// repository-root example cases of the shell reader that module root runs
// replace, with its files list, for each module root its expected.json
// lists, and compares the whole report.
func TestAffectedModuleRootFixture(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "affected", "module-root")
	dir := setupOracle(t, src)
	data, err := os.ReadFile(filepath.Join(src, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ModuleRoot string         `json:"module_root"`
		Report     AffectedReport `json:"report"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("expected.json: %v", err)
	}
	for _, c := range cases {
		t.Run(c.ModuleRoot, func(t *testing.T) {
			r, err := Affected(AffectedOptions{Head: filepath.Join(dir, "head"), Diff: filepath.Join(dir, "files.json"),
				ModuleRoot: c.ModuleRoot, Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			checkAffectedSchema(t, r)
			if !reflect.DeepEqual(*r, c.Report) {
				t.Errorf("report:\n%s\nwant:\n%s", mustJSON(t, r), mustJSON(t, c.Report))
			}
		})
	}
}

// rootRepo is a repository with a module root modules/a, a sibling whose name
// starts with it, a wrapper, a submodule of the root and repository-root
// examples.
var rootRepo = map[string]string{
	"README.md":                      "# repo\n",
	".github/workflows/ci.yml":       "on: push\n",
	"modules/a/main.tf":              "variable \"v\" {}\n",
	"modules/a/README.md":            "# a\n",
	"modules/a/modules/sub/main.tf":  "variable \"s\" {}\n",
	"modules/a/examples/own/main.tf": "module \"a\" {\n  source = \"../../\"\n}\n",
	"modules/ab/main.tf":             "variable \"v\" {}\n",
	"modules/b/main.tf":              "variable \"v\" {}\n",
	"modules/wrap/main.tf":           "module \"a\" {\n  source = \"../a\"\n}\n",
	"examples/direct/main.tf":        "module \"a\" {\n  source = \"../../modules/a\"\n}\n",
	"examples/wrapped/main.tf":       "module \"w\" {\n  source = \"../../modules/wrap\"\n}\n",
	"examples/sub/main.tf":           "module \"s\" {\n  source = \"../../modules/a/modules/sub\"\n}\n",
	"examples/sibling/main.tf":       "module \"ab\" {\n  source = \"../../modules/ab\"\n}\n",
	"examples/hub/main.tf":           "module \"a\" {\n  source = \"../../modules/a\"\n}\nmodule \"b\" {\n  source = \"../../modules/b\"\n}\n",
	"examples/registry/main.tf":      "module \"vpc\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"6.0.0\"\n}\n",
}

func runModuleRoot(t *testing.T, head, base map[string]string, root string) *AffectedReport {
	t.Helper()
	o := AffectedOptions{Head: writeTree(t, head), ModuleRoot: root, Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	r, err := Affected(o)
	if err != nil {
		t.Fatal(err)
	}
	checkAffectedSchema(t, r)
	if r.ModuleRoot != root {
		t.Errorf("module_root = %q, want %q", r.ModuleRoot, root)
	}
	return r
}

// rootPairs renders the examples as "reason dir", with " unresolved" when set.
func rootPairs(r *AffectedReport) []string {
	var out []string
	for _, e := range r.Examples {
		s := e.Reason + " " + e.Dir
		if e.Unresolved {
			s += " unresolved"
		}
		out = append(out, s)
	}
	return out
}

func wantRoot(t *testing.T, r *AffectedReport, inert bool, fallback string, changed []string, examples ...string) {
	t.Helper()
	got := ""
	if r.Fallback != nil {
		got = *r.Fallback
	}
	if r.Inert != inert || got != fallback {
		t.Errorf("inert %v fallback %q, want %v %q", r.Inert, got, inert, fallback)
	}
	if changed != nil && !slices.Equal(r.Changed, changed) {
		t.Errorf("changed = %q, want %q", r.Changed, changed)
	}
	if !slices.Equal(rootPairs(r), examples) {
		t.Errorf("examples = %q, want %q", rootPairs(r), examples)
	}
}

func TestAffectedModuleRootSelection(t *testing.T) {
	t.Run("a change of the root reaches direct, wrapped and own callers; a call into a directory below it counts", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}), rootRepo, "modules/a")
		wantRoot(t, r, false, "", []string{"modules/a/main.tf"},
			"caller examples/direct", "caller examples/hub", "caller examples/wrapped", "caller modules/a/examples/own", "all examples/sub")
	})
	t.Run("a change of a submodule of the root ranks the example that calls it", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{"modules/a/modules/sub/main.tf": "variable \"t\" {}\n"}), rootRepo, "modules/a")
		wantRoot(t, r, false, "", []string{"modules/a/modules/sub/main.tf"},
			"caller examples/sub", "all examples/direct", "all examples/hub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("a wrapper on the way to the root is in scope", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{"modules/wrap/main.tf": "module \"a\" {\n  source = \"../a/\"\n}\n"}), rootRepo, "modules/a")
		wantRoot(t, r, false, "", []string{"modules/wrap/main.tf"},
			"caller examples/wrapped", "all examples/direct", "all examples/hub", "all examples/sub", "all modules/a/examples/own")
	})
	t.Run("a touched root example comes first", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{"examples/direct/main.tf": "module \"a\" {\n  source = \"./../../modules/a/\"\n}\n"}), rootRepo, "modules/a")
		wantRoot(t, r, false, "", []string{"examples/direct/main.tf"},
			"touched examples/direct", "all examples/hub", "all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("a sibling whose name starts with the root, a hub's other module and out-of-scope paths stay out", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{
			"modules/ab/main.tf": "variable \"x\" {}\n", "modules/b/main.tf": "variable \"x\" {}\n",
			".github/workflows/ci.yml": "on: pull_request\n", "README.md": "# changed\n",
		}), rootRepo, "modules/a")
		wantRoot(t, r, true, "", []string{},
			"all examples/direct", "all examples/hub", "all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("a documentation change of the root is inert and ranks no caller", func(t *testing.T) {
		r := runModuleRoot(t, with(rootRepo, map[string]string{"modules/a/README.md": "# a, changed\n"}), rootRepo, "modules/a")
		wantRoot(t, r, true, "", []string{"modules/a/README.md"},
			"all examples/direct", "all examples/hub", "all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("a file a root example reads is in scope", func(t *testing.T) {
		head := with(rootRepo, map[string]string{
			"examples/direct/main.tf": "module \"a\" {\n  source = \"../../modules/a\"\n}\nlocals {\n  p = file(\"../../policies/p.json\")\n}\n",
			"policies/p.json":         "{}\n",
		})
		r := runModuleRoot(t, with(head, map[string]string{"policies/p.json": "{\"a\": 1}\n"}), head, "modules/a")
		wantRoot(t, r, false, "", []string{"policies/p.json"},
			"caller examples/direct", "all examples/hub", "all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("an own example that calls a sibling module puts it in scope", func(t *testing.T) {
		head := with(rootRepo, map[string]string{"modules/a/examples/own/main.tf": "module \"a\" {\n  source = \"../../\"\n}\nmodule \"b\" {\n  source = \"../../../b\"\n}\n"})
		r := runModuleRoot(t, with(head, map[string]string{"modules/b/main.tf": "variable \"y\" {}\n"}), head, "modules/a")
		wantRoot(t, r, false, "", []string{"modules/b/main.tf"},
			"caller examples/hub", "caller modules/a/examples/own", "all examples/direct", "all examples/sub", "all examples/wrapped")
	})
	t.Run("a removed root example file is a change of it", func(t *testing.T) {
		base := with(rootRepo, map[string]string{"examples/direct/extra.tf": "locals {}\n"})
		r := runModuleRoot(t, rootRepo, base, "modules/a")
		wantRoot(t, r, false, "", []string{"examples/direct/extra.tf"},
			"touched examples/direct", "all examples/hub", "all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
}

func TestAffectedModuleRootUnresolved(t *testing.T) {
	change := map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}
	run := func(t *testing.T, set map[string]string) *AffectedReport {
		head := with(rootRepo, set)
		return runModuleRoot(t, with(head, change), head, "modules/a")
	}
	callers := []string{"caller examples/direct", "caller examples/hub", "caller examples/wrapped", "caller modules/a/examples/own"}
	for name, body := range map[string]string{
		"a dynamic source":            "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n",
		"a file that does not parse":  strings.Repeat("<", 4096),
		"a file over the token limit": strings.Repeat("variable \"v\" {}\n", 30000),
	} {
		t.Run(name+" in a root example makes it unresolved and the change not inert", func(t *testing.T) {
			r := run(t, map[string]string{"examples/odd/main.tf": body})
			wantRoot(t, r, false, "", nil, append(slices.Clone(callers), "all examples/odd unresolved", "all examples/sub")...)
		})
	}
	t.Run("a .tf file that is a link or over 1 MiB in a root example makes it unresolved", func(t *testing.T) {
		r := run(t, map[string]string{"examples/odd/main.tf": "->../direct/main.tf", "examples/big/main.tf": "# " + strings.Repeat("x", 1<<20) + "\n"})
		wantRoot(t, r, false, "", nil, append(slices.Clone(callers), "all examples/big unresolved", "all examples/odd unresolved", "all examples/sub")...)
	})
	t.Run("a root example that reaches the root through a directory with a problem stays a caller", func(t *testing.T) {
		r := run(t, map[string]string{"examples/direct/main.tf": "module \"a\" {\n  source = \"../../modules/a\"\n}\nmodule \"c\" {\n  source = \"../../modules/c\"\n}\n",
			"modules/c/main.tf": "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n"})
		wantRoot(t, r, false, "", nil, append(slices.Clone(callers), "all examples/sub")...)
		head := with(rootRepo, map[string]string{"examples/direct/main.tf": "module \"c\" {\n  source = \"../../modules/c\"\n}\nmodule \"a\" {\n  source = \"../../modules/a\"\n}\n",
			"modules/c/main.tf": "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n"})
		r = runModuleRoot(t, with(head, map[string]string{"modules/a/README.md": "# a, changed\n"}), head, "modules/a")
		wantRoot(t, r, false, "", []string{"modules/a/README.md"}, "all examples/direct unresolved", "all examples/hub",
			"all examples/sub", "all examples/wrapped", "all modules/a/examples/own")
	})
	t.Run("a problem only an unrelated module holds is ignored", func(t *testing.T) {
		r := run(t, map[string]string{"modules/c/main.tf": "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n", "modules/d/main.tf": "<<<"})
		wantRoot(t, r, false, "", nil, append(slices.Clone(callers), "all examples/sub")...)
	})
	t.Run("a link, an escape, a comment, a heredoc and a resource source are no call and no problem", func(t *testing.T) {
		r := run(t, map[string]string{
			"alink":                     "->modules/a",
			"examples/vialink/main.tf":  "module \"a\" {\n  source = \"../../alink\"\n}\n",
			"examples/escape/main.tf":   "module \"a\" {\n  source = \"../../../x/modules/a\"\n}\n",
			"examples/comment/main.tf":  "# module \"a\" { source = \"../../modules/a\" }\n/* module \"a\" {\n  source = \"../../modules/a\"\n} */\n",
			"examples/heredoc/main.tf":  "locals {\n  x = <<-EOT\n    module \"a\" {\n      source = \"../../modules/a\"\n    }\n    EOT\n}\n",
			"examples/resource/main.tf": "resource \"aws_s3_object\" \"o\" {\n  source = \"../../modules/a\"\n}\n",
			"examples/linked":           "->direct",
			"modules/a/x":               "->../b",
			"examples/inlink/main.tf":   "module \"x\" {\n  source = \"../../modules/a/x\"\n}\n",
		})
		wantRoot(t, r, false, "", nil, append(slices.Clone(callers), "all examples/sub")...)
	})
}

func TestAffectedModuleRootFallback(t *testing.T) {
	roots := []string{"all examples/direct", "all examples/hub", "all examples/registry", "all examples/sibling",
		"all examples/sub", "all examples/wrapped", "all modules/a/examples/own"}
	t.Run("a dynamic source in the root's closure falls back with every root example", func(t *testing.T) {
		head := with(rootRepo, map[string]string{"modules/a/main.tf": "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n"})
		r := runModuleRoot(t, with(head, map[string]string{"modules/b/main.tf": "variable \"q\" {}\n"}), head, "modules/a")
		wantRoot(t, r, false, FallbackDynamicSource, []string{}, roots...)
	})
	t.Run("a file of the root that does not parse falls back", func(t *testing.T) {
		head := with(rootRepo, map[string]string{"modules/a/broken.tf": "<<<"})
		r := runModuleRoot(t, with(head, map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}), head, "modules/a")
		wantRoot(t, r, false, FallbackUnparsed, []string{"modules/a/main.tf"}, roots...)
	})
	t.Run("no change input falls back", func(t *testing.T) {
		r := runModuleRoot(t, rootRepo, nil, "modules/a")
		wantRoot(t, r, false, FallbackNoChangeInput, []string{}, roots...)
	})
}

func TestAffectedModuleRootCaps(t *testing.T) {
	change := map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}
	callers := []string{"caller examples/direct", "caller examples/hub", "caller examples/wrapped", "caller modules/a/examples/own"}
	all := []string{"caller modules/a/examples/own", "all examples/direct unresolved", "all examples/hub unresolved",
		"all examples/many unresolved", "all examples/registry unresolved", "all examples/sibling unresolved",
		"all examples/sub unresolved", "all examples/wrapped unresolved"}
	files := func(n, size int) map[string]string {
		set := map[string]string{}
		for i := range n {
			set[fmt.Sprintf("examples/many/f%04d.tf", i)] = strings.Repeat("#", size-1) + "\n"
		}
		return set
	}
	own := 0 // .tf files of rootRepo's root examples, and their bytes
	ownBytes := 0
	for p, body := range rootRepo {
		if strings.HasPrefix(p, "examples/") && strings.HasSuffix(p, ".tf") {
			own++
			ownBytes += len(body)
		}
	}
	run := func(set map[string]string) *AffectedReport {
		head := with(rootRepo, set)
		return runModuleRoot(t, with(head, change), head, "modules/a")
	}
	t.Run("at 1000 files the root examples are resolved", func(t *testing.T) {
		wantRoot(t, run(files(rootExampleMaxFiles-own, 1)), false, "", nil, append(slices.Clone(callers), "all examples/sub")...)
	})
	t.Run("past 1000 files every root example is unresolved", func(t *testing.T) {
		wantRoot(t, run(files(rootExampleMaxFiles-own+1, 1)), false, "", nil, all...)
	})
	t.Run("at 8 MiB the root examples are resolved", func(t *testing.T) {
		set := files(8, (rootExampleMaxBytes-ownBytes)/8)
		set["examples/many/pad.tf"] = strings.Repeat("#", rootExampleMaxBytes-ownBytes-8*((rootExampleMaxBytes-ownBytes)/8)) + ""
		if set["examples/many/pad.tf"] == "" {
			delete(set, "examples/many/pad.tf")
		}
		wantRoot(t, run(set), false, "", nil, append(slices.Clone(callers), "all examples/sub")...)
	})
	t.Run("past 8 MiB every root example is unresolved", func(t *testing.T) {
		wantRoot(t, run(files(9, (rootExampleMaxBytes-ownBytes)/8)), false, "", nil, all...)
	})
}

func TestAffectedModuleRootRefused(t *testing.T) {
	head := with(rootRepo, map[string]string{"linked/main.tf": "", "alias": "->modules/a"})
	delete(head, "linked/main.tf")
	dir := writeTree(t, head)
	for _, m := range []string{".", "..", "/modules/a", "modules/a/", "modules//a", "./modules/a", "modules/../modules/a",
		"modules\\a", "modules/none", "alias", "examples", "examples/direct", "modules/a/main.tf", "mod ules", "modules/a\n"} {
		if _, err := Affected(AffectedOptions{Head: dir, Diff: "", ModuleRoot: m, Limits: DefaultLimits}); err == nil {
			t.Errorf("module root %q: no error", m)
		}
	}
	bad := writeTree(t, with(rootRepo, map[string]string{"examples/b@d/main.tf": "locals {}\n"}))
	if _, err := Affected(AffectedOptions{Head: bad, ModuleRoot: "modules/a", Limits: DefaultLimits}); err == nil {
		t.Error("a root example name outside [A-Za-z0-9._-]: no error")
	}
}

func TestAffectedWithoutModuleRootHasNoField(t *testing.T) {
	r := runAffected(t, affectedHead, nil)
	if s := string(mustJSON(t, r)); strings.Contains(s, "module_root") || strings.Contains(s, "unresolved") {
		t.Errorf("report without --module-root names module_root or unresolved: %s", s)
	}
}
