package behaviour

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

const (
	neRoot = "variable \"cidr\" {\n  type    = string\n  default = null\n}\n" +
		"resource \"aws_vpc\" \"this\" {\n  cidr_block = var.cidr\n}\n"
	neOld = "module \"vpc\" {\n  source = \"../../\"\n  cidr   = \"10.0.0.0/16\"\n}\n"
	neNew = "provider \"aws\" {\n  region = \"eu-west-1\"\n}\n" +
		"locals {\n  name = \"ipv6-ipam\"\n}\n" +
		"module \"vpc\" {\n  source = \"../../\"\n  cidr   = var.pool\n}\n"
	neVars = "variable \"pool\" {\n  type = string\n}\n"
	neOuts = "output \"id\" {\n  value = module.vpc\n}\n"
)

func neBase() map[string]string {
	return map[string]string{"main.tf": neRoot, "examples/old/main.tf": neOld, "README.md": "See examples/ipv6-ipam.\n"}
}

func neHead(extra map[string]string) map[string]string {
	h := neBase()
	h["examples/ipv6-ipam/main.tf"] = neNew
	h["examples/ipv6-ipam/variables.tf"] = neVars
	h["examples/ipv6-ipam/outputs.tf"] = neOuts
	h["examples/ipv6-ipam/README.md"] = "# new\n"
	maps.Copy(h, extra)
	return h
}

// newExampleFiles lists the files of triggers with the new example reason,
// and fails on any such trigger that selects a level or has an address.
func newExampleFiles(t *testing.T, r *Report) []string {
	t.Helper()
	var out []string
	for _, tr := range r.Triggers {
		if str(tr.Reason) != reasonNewExample {
			continue
		}
		if len(tr.Levels) != 0 || tr.Address != nil || tr.Category != CatUnclassified || tr.Line != nil {
			t.Fatalf("new example trigger %+v", tr)
		}
		out = append(out, tr.File)
	}
	return out
}

func TestNewExampleDirectory(t *testing.T) {
	t.Parallel()
	// A change elsewhere keeps its own trigger beside the new example's.
	r := runPair(t, neBase(), neHead(map[string]string{"examples/old/main.tf": strings.Replace(neOld, "10.0.0.0/16", "10.1.0.0/16", 1)}), "")
	want := []string{"examples/ipv6-ipam/main.tf", "examples/ipv6-ipam/outputs.tf", "examples/ipv6-ipam/variables.tf"}
	if got := newExampleFiles(t, r); !slices.Equal(got, want) {
		t.Fatalf("new example triggers %v, want %v", got, want)
	}
	if len(r.Triggers) != len(want)+1 || r.Inert {
		t.Fatalf("triggers %d inert %v, want %d and not inert", len(r.Triggers), r.Inert, len(want)+1)
	}
	for _, e := range r.Entries {
		if !strings.HasPrefix(e.Path, "examples/ipv6-ipam/") {
			continue
		}
		if e.Path == "examples/ipv6-ipam/README.md" {
			if !e.Inert {
				t.Fatalf("README of the new example not inert: %+v", e)
			}
			continue
		}
		if e.Inert || len(e.TriggerIDs) != 1 {
			t.Fatalf("entry %+v: want one trigger, not inert", e)
		}
	}
	// Each such trigger stays unverifiable, whatever the ladder planned.
	l := &Ladder{Coverage: Coverage{Class: "complete", Examples: []CoverageExample{{Dir: "examples/ipv6-ipam", Head: "planned",
		Addresses: []string{"module.vpc.aws_vpc.this"}, ModuleCalls: map[string]string{"vpc": "."}}}}}
	for _, tr := range r.Triggers {
		if str(tr.Reason) != reasonNewExample {
			continue
		}
		if ts := evalTrigger(tr, l, nil, nil, map[string]bool{}, false); ts.State != StateUnverifiable {
			t.Fatalf("%s: state %s, want unverifiable", tr.File, ts.State)
		}
	}
}

func TestNewExampleKeepsTriggers(t *testing.T) {
	t.Parallel()
	called := "module \"ex\" {\n  source = \"./examples/ipv6-ipam\"\n}\n"
	cases := []struct {
		name       string
		base, head map[string]string
	}{
		{"called from the root at the head", neBase(), neHead(map[string]string{"main.tf": neRoot + called})},
		{"named at the base", func() map[string]string {
			b := neBase()
			b["main.tf"] = neRoot + "# ipv6-ipam\n"
			return b
		}(), neHead(nil)},
		{"named by a test file", neBase(), neHead(map[string]string{"tests/a.tftest.hcl": "run \"a\" {\n  module {\n    source = \"./examples/ipv6-ipam\"\n  }\n}\n"})},
		{"named by a wrapper file", neBase(), neHead(map[string]string{"terragrunt.hcl": "terraform {\n  source = \"examples//ipv6-ipam\"\n}\n"})},
		{"named by a JSON configuration file", neBase(), neHead(map[string]string{"x.tf.json": "{\"module\": {\"e\": {\"source\": \"./examples/ipv6-ipam\"}}}\n"})},
		{"named by a variables file", neBase(), neHead(map[string]string{"examples/old/x.tfvars": "p = \"ipv6-ipam\"\n"})},
		{"present at the base", func() map[string]string {
			b := neBase()
			b["examples/ipv6-ipam/README.md"] = "# new\n"
			return b
		}(), neHead(nil)},
		{"a configuration file whose content is not read", neBase(),
			neHead(map[string]string{"examples/old/big.tf": "# " + strings.Repeat("x", 3<<20) + "\n"})},
		{"outside the examples root", neBase(), func() map[string]string {
			h := neBase()
			h["modules/ipv6-ipam/main.tf"] = neNew
			return h
		}()},
		{"a file renamed into it", map[string]string{"main.tf": neRoot, "examples/old/main.tf": neOld, "examples/gone/variables.tf": neVars},
			neHead(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := runPair(t, tc.base, tc.head, "")
			if got := newExampleFiles(t, r); len(got) != 0 {
				t.Fatalf("new example triggers %v, want none", got)
			}
		})
	}
}

func TestNewExampleLinkKeepsTriggers(t *testing.T) {
	t.Parallel()
	for _, where := range []string{"examples/ipv6-ipam/link.tf", "examples/old/link"} {
		head := writeTree(t, neHead(nil))
		if err := os.Symlink("main.tf", filepath.Join(head, filepath.FromSlash(where))); err != nil {
			t.Fatal(err)
		}
		r, err := Run(Options{Head: head, Base: writeTree(t, neBase()), Limits: facts.DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if got := newExampleFiles(t, r); len(got) != 0 {
			t.Fatalf("%s: new example triggers %v, want none", where, got)
		}
	}
}

func TestNewExampleGitlinkKeepsTriggers(t *testing.T) {
	t.Parallel()
	for _, side := range []string{"base", "head"} {
		base, head := writeTree(t, neBase()), writeTree(t, neHead(nil))
		root := map[string]string{"base": base, "head": head}[side]
		if err := os.MkdirAll(filepath.Join(root, "vendor", "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		r, err := Run(Options{Head: head, Base: base, Limits: facts.DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if got := newExampleFiles(t, r); len(got) != 0 {
			t.Fatalf("gitlink at the %s: new example triggers %v, want none", side, got)
		}
	}
}

// A local module the new example calls is named by the example, so it
// keeps its own triggers; the example itself is still new.
func TestNewExampleNestedModule(t *testing.T) {
	t.Parallel()
	head := neHead(map[string]string{
		"examples/ipv6-ipam/main.tf":             neNew + "module \"sub\" {\n  source = \"./modules/sub\"\n}\n",
		"examples/ipv6-ipam/modules/sub/main.tf": "resource \"aws_s3_bucket\" \"b\" {}\n",
	})
	r := runPair(t, neBase(), head, "")
	got := newExampleFiles(t, r)
	if !slices.Contains(got, "examples/ipv6-ipam/main.tf") || slices.Contains(got, "examples/ipv6-ipam/modules/sub/main.tf") {
		t.Fatalf("new example triggers %v", got)
	}
}
