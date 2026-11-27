package facts

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// OpenTofu reads .tofu and .tofu.json files, and a .tofu file in place of a
// .tf file of its name, so a directory with one is never read as complete.
var tofuFiles = map[string]string{
	"main.tofu":      "locals {\n  r = file(\"${path.module}/README.md\")\n}\n",
	"main.tofu.json": "{\"locals\": {\"r\": \"${file(\\\"${path.module}/README.md\\\")}\"}}\n",
}

// TestReadSitesOpenTofu checks a .tofu or .tofu.json file makes the
// read-site scan incomplete, as a .tf.json file does, while a hidden one,
// which neither tool loads, does not.
func TestReadSitesOpenTofu(t *testing.T) {
	t.Parallel()
	for name, src := range tofuFiles {
		for dir, complete := range map[string]bool{"": false, "modules/m/": false, "modules/m/.": true} {
			t.Run(dir+name, func(t *testing.T) {
				t.Parallel()
				tree := mustTree(t, writeTree(t, map[string]string{"main.tf": "", "README.md": "# r\n", dir + name: src}))
				_, got, err := ReadSites(tree, DefaultLimits)
				if err != nil || got != complete {
					t.Errorf("complete %v, err %v; want complete %v", got, err, complete)
				}
			})
		}
	}
}

// TestKeepHeldBounded checks the held IAM path literals stop at the
// deadline and at the read-site cap, and are kept below both.
func TestKeepHeldBounded(t *testing.T) {
	t.Parallel()
	tree := mustTree(t, writeTree(t, map[string]string{"main.tf": ""}))
	site := ReadSite{File: "main.tf", Line: 1, Kind: "path", Anchor: AnchorRoot, Dir: ".", Text: "/", Exact: true}
	held := []heldSite{{to: flowHolder{opaque: true}, sites: []ReadSite{site}}}
	sites, complete, err := keepHeld(tree, []ReadSite{site}, held, flowUses{}, map[string]bool{}, DefaultLimits)
	if err != nil || !complete || len(sites) != 2 {
		t.Errorf("within the limits: %d sites, complete %v, err %v", len(sites), complete, err)
	}
	lim := DefaultLimits
	lim.maxReadSites = 1
	if sites, complete, err := keepHeld(tree, []ReadSite{site}, held, flowUses{}, map[string]bool{}, lim); err != nil || complete || sites != nil {
		t.Errorf("past the cap: %d sites, complete %v, err %v", len(sites), complete, err)
	}
	lim = DefaultLimits
	lim.Deadline = time.Now().Add(-time.Second)
	if _, _, err := keepHeld(tree, nil, held, flowUses{}, map[string]bool{}, lim); !errors.Is(err, ErrDeadline) {
		t.Errorf("past the deadline: err %v, want ErrDeadline", err)
	}
}

// TestReadSitesHeldCap checks ReadSites gives up once the IAM path literals
// it keeps pass the read-site cap.
func TestReadSitesHeldCap(t *testing.T) {
	t.Parallel()
	src := "locals {\n  a = \"/\"\n  b = \"/\"\n}\noutput \"a\" {\n  value = local.a\n}\noutput \"b\" {\n  value = local.b\n}\n"
	tree := mustTree(t, writeTree(t, map[string]string{"main.tf": src}))
	if sites, complete, err := ReadSites(tree, DefaultLimits); err != nil || !complete || len(sites) != 2 {
		t.Fatalf("default cap: %d sites, complete %v, err %v", len(sites), complete, err)
	}
	lim := DefaultLimits
	lim.maxReadSites = 1
	if _, complete, err := ReadSites(tree, lim); err != nil || complete {
		t.Errorf("past the cap: complete %v, err %v", complete, err)
	}
}

// TestReadSitesLinkedConfig checks a configuration file that is a link
// makes the scan incomplete.
func TestReadSitesLinkedConfig(t *testing.T) {
	t.Parallel()
	tree := mustTree(t, writeTree(t, map[string]string{"main.tf": "->real.txt", "real.txt": "locals {}\n"}))
	if _, complete, err := ReadSites(tree, DefaultLimits); err != nil || complete {
		t.Errorf("complete %v, err %v", complete, err)
	}
}

// TestAffectedOpenTofu checks a .tofu or .tofu.json file falls back to
// unparsed, and a removed one is a change of its directory.
func TestAffectedOpenTofu(t *testing.T) {
	t.Parallel()
	all := []string{"all examples/chain", "all examples/complete", "all examples/registry", "all examples/sub"}
	for name, src := range tofuFiles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := with(affectedHead, map[string]string{name: src})
			wantAffected(t, runAffected(t, with(base, map[string]string{"README.md": "# changed\n"}), base), false, FallbackUnparsed, all...)
			base = with(affectedHead, map[string]string{"modules/sub/" + name: src})
			wantAffected(t, runAffected(t, affectedHead, base), false, "",
				"caller examples/sub", "all examples/chain", "all examples/complete", "all examples/registry")
		})
	}
}

// TestAffectedModuleRootOpenTofuExample checks a root example that holds
// only an OpenTofu file is listed, unresolved.
func TestAffectedModuleRootOpenTofuExample(t *testing.T) {
	t.Parallel()
	head := with(rootRepo, map[string]string{"examples/odd/main.tofu": "module \"a\" {\n  source = \"../../modules/a\"\n}\n"})
	r := runModuleRoot(t, with(head, map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}), head, "modules/a")
	if !slices.Contains(rootPairs(r), "all examples/odd unresolved") {
		t.Errorf("examples %q, want examples/odd unresolved", rootPairs(r))
	}
	// A directory with no configuration is no example, whatever its name.
	head = with(rootRepo, map[string]string{"examples/my notes/README.md": "# notes\n"})
	r = runModuleRoot(t, with(head, map[string]string{"modules/a/main.tf": "variable \"w\" {}\n"}), head, "modules/a")
	if slices.ContainsFunc(rootPairs(r), func(p string) bool { return strings.Contains(p, "examples/my notes") }) {
		t.Errorf("examples %q list a directory with no configuration", rootPairs(r))
	}
}

// TestParseDirOpenTofu checks a .tofu or .tofu.json file leaves the
// directory's configuration rules undecidable.
func TestParseDirOpenTofu(t *testing.T) {
	t.Parallel()
	for name, src := range tofuFiles {
		tree := mustTree(t, writeTree(t, map[string]string{"main.tf": "", name: src}))
		m, err := ParseDir(tree, ".", DefaultLimits)
		if err != nil || m.reasonCode() != ReasonUnsupportedFile {
			t.Errorf("%s: reason %q, err %v", name, m.reasonCode(), err)
		}
	}
}

// TestConfigNameOpenTofu checks the lint helpers count an OpenTofu file as
// configuration: an area, a configured example and a module base.
func TestConfigNameOpenTofu(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"main.tofu", "main.tofu.json"} {
		tree := mustTree(t, writeTree(t, map[string]string{"infra/net/" + name: "", "examples/e/" + name: "", "modules/m/" + name: ""}))
		if mods, err := discoverModules(tree, nil, DefaultLimits); err != nil || !mods["infra/net"] {
			t.Errorf("%s: discovered %v, err %v", name, mods, err)
		}
		if !holdsTerraform(tree, "examples/e") {
			t.Errorf("%s: examples/e holds no Terraform", name)
		}
		if newModuleBase(tree) {
			t.Errorf("%s: base read as a new module", name)
		}
		for _, p := range []string{"examples/e/" + name, "README.md"} {
			if !newModuleBase(mustTree(t, writeTree(t, map[string]string{p: ""}))) {
				t.Errorf("a base of only %s is no new module", p)
			}
		}
		if a, ok := unreadArea("tests/"+name, &Entry{Kind: KindFile}); !ok || a != rootArea {
			t.Errorf("%s: unread area %q %v", name, a, ok)
		}
		if InertDocPath("docs/" + name) {
			t.Errorf("docs/%s is inert", name)
		}
	}
}
