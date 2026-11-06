package facts

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The monorepo fixture: one module in infra/modules/network called from
// live/prod and live/stage. The change removes its variable name and both
// callers stop setting it.
const (
	netBase   = "variable \"cidr\" {\n  type = string\n}\n\nvariable \"name\" {\n  type = string\n}\n"
	netHead   = "variable \"cidr\" {\n  type = string\n}\n"
	callBase  = "module \"net\" {\n  source = \"../../infra/modules/network\"\n  cidr   = \"10.0.0.0/16\"\n  name   = \"a\"\n}\n"
	callHead  = "module \"net\" {\n  source = \"../../infra/modules/network\"\n  cidr   = \"10.0.0.0/16\"\n}\n"
	netModule = "infra/modules/network"
)

func monoBase() map[string]string {
	return map[string]string{
		"infra/modules/network/variables.tf": netBase,
		"live/prod/main.tf":                  callBase,
		"live/stage/main.tf":                 callBase,
	}
}

func monoHead() map[string]string {
	return map[string]string{
		"infra/modules/network/variables.tf": netHead,
		"live/prod/main.tf":                  callHead,
		"live/stage/main.tf":                 callHead,
	}
}

// withFiles returns a copy of m with the given paths set, an empty value
// deleting the path.
func withFiles(m map[string]string, kv ...string) map[string]string {
	out := maps.Clone(m)
	for ; len(kv) >= 2; kv = kv[2:] {
		if k, v := kv[0], kv[1]; v == "" {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return out
}

func runMono(t *testing.T, head, base map[string]string, profileDir string, fullTree bool) *Report {
	t.Helper()
	o := Options{Head: writeTree(t, head), ProfileDir: profileDir, FullTree: fullTree, Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	r, err := run(o, Rules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func callersOf(r *Report, module string) *ModuleCallers {
	for i := range r.ModuleCallers {
		if r.ModuleCallers[i].Module == module {
			return &r.ModuleCallers[i]
		}
	}
	return nil
}

func TestModuleCallersMonorepo(t *testing.T) {
	r := runMono(t, monoHead(), monoBase(), writeProfile(t, "format: 1", "consumers: in-repo", "partitions: portable", "examples: required", "release: registry"), false)
	if want := []string{netModule, "live/prod", "live/stage"}; !slices.Equal(r.Scope.ModuleAreas, want) {
		t.Errorf("module_areas %q, want %q", r.Scope.ModuleAreas, want)
	}
	m := callersOf(r, netModule)
	if m == nil {
		t.Fatalf("no module_callers entry for %s: %+v", netModule, r.ModuleCallers)
	}
	want := []ModuleCaller{
		{Area: "live/prod", File: "live/prod/main.tf", Line: 2, FileChanged: true},
		{Area: "live/stage", File: "live/stage/main.tf", Line: 2, FileChanged: true},
	}
	if !m.Complete || !reflect.DeepEqual(m.Callers, want) {
		t.Errorf("entry %+v, want complete with callers %+v", *m, want)
	}
	if !slices.IsSortedFunc(r.ModuleCallers, func(a, b ModuleCallers) int { return strings.Compare(a.Module, b.Module) }) {
		t.Error("module_callers is not sorted by module")
	}
	for _, e := range r.ModuleCallers {
		if e.Complete != m.Complete {
			t.Error("complete differs between entries")
		}
	}
	if !slices.Equal(r.InRepoDowngrade, []string{netModule}) {
		t.Errorf("in_repo_downgrade %q, want [%s]", r.InRepoDowngrade, netModule)
	}
	marked := 0
	for _, c := range r.Candidates {
		area, _ := AreaOf(c.File, r.Scope.ModuleAreas)
		listed := area == netModule && slices.Contains(downgradeIDs(), c.RuleID)
		switch {
		case listed && (c.Downgrade != DowngradeInRepo || !slices.Equal(c.Severities, []string{"MEDIUM"})):
			t.Errorf("candidate %s %s: downgrade %q severities %q, want in-repo [MEDIUM]", c.RuleID, c.File, c.Downgrade, c.Severities)
		case !listed && (c.Downgrade != "" || c.Severities != nil):
			t.Errorf("candidate %s %s carries a downgrade", c.RuleID, c.File)
		case listed:
			marked++
		}
	}
	if marked == 0 {
		t.Error("no candidate carries the downgrade")
	}
}

// TestInRepoDowngradeConditions checks every condition of the downgrade:
// the profile, the change, full_tree, complete and every caller changed.
func TestInRepoDowngradeConditions(t *testing.T) {
	inRepo := writeProfile(t, "format: 1", "consumers: in-repo", "partitions: portable", "examples: required", "release: registry")
	for name, c := range map[string]struct {
		head, base map[string]string
		profile    string
		fullTree   bool
	}{
		"public consumers":          {monoHead(), monoBase(), testProfile, false},
		"no profile":                {monoHead(), monoBase(), "", false},
		"full tree":                 {monoHead(), monoBase(), inRepo, true},
		"caller file unchanged":     {withFiles(monoHead(), "live/stage/main.tf", callBase), monoBase(), inRepo, false},
		"incomplete":                {withFiles(monoHead(), "live/dev/main.tf", "module \"x\" {\n  source = \"lib/x\"\n}\n"), monoBase(), inRepo, false},
		"no caller":                 {withFiles(monoHead(), "live/prod/main.tf", "", "live/stage/main.tf", ""), withFiles(monoBase(), "live/prod/main.tf", "", "live/stage/main.tf", ""), inRepo, false},
		"change none, no base":      {monoHead(), nil, inRepo, false},
		"one caller, one unchanged": {withFiles(monoHead(), "live/stage/main.tf", ""), withFiles(monoBase(), "live/stage/main.tf", "", "live/prod/main.tf", callHead), inRepo, false},
	} {
		t.Run(name, func(t *testing.T) {
			r := runMono(t, c.head, c.base, c.profile, c.fullTree)
			if len(r.InRepoDowngrade) != 0 {
				t.Errorf("in_repo_downgrade %q, want []", r.InRepoDowngrade)
			}
			if r.InRepoDowngrade == nil {
				t.Error("in_repo_downgrade is nil, want []")
			}
			for _, cd := range r.Candidates {
				if cd.Downgrade != "" || cd.Severities != nil {
					t.Errorf("candidate %s carries a downgrade", cd.RuleID)
				}
			}
		})
	}
}

func TestModuleCallersChangeNone(t *testing.T) {
	r := runMono(t, monoHead(), nil, testProfile, false)
	if r.ModuleCallers == nil || len(r.ModuleCallers) != 0 {
		t.Errorf("module_callers %+v, want []", r.ModuleCallers)
	}
	if r.Scope.ModuleAreas == nil {
		t.Error("module_areas is nil, want a list")
	}
}

func TestModuleCallersNoModuleAreas(t *testing.T) {
	r := runMono(t, map[string]string{"main.tf": "variable \"a\" {}\n"}, map[string]string{"main.tf": "\n"}, testProfile, false)
	if r.Scope.ModuleAreas == nil || len(r.Scope.ModuleAreas) != 0 {
		t.Errorf("module_areas %q, want []", r.Scope.ModuleAreas)
	}
}

// TestModuleCallersResolution covers how a caller's source resolves.
func TestModuleCallersResolution(t *testing.T) {
	call := func(src string) string { return "module \"x\" {\n  source = " + src + "\n}\n" }
	for name, c := range map[string]struct {
		extra    map[string]string
		complete bool
		caller   *ModuleCaller // a caller of netModule the extra files add
	}{
		"nested target that is not an area": {map[string]string{"live/dev/main.tf": call(`"../../infra/modules/network/sub"`)}, false, nil},
		"sibling call": {map[string]string{"infra/modules/vpc/main.tf": call(`"../network"`)}, true,
			&ModuleCaller{Area: "infra/modules/vpc", File: "infra/modules/vpc/main.tf", Line: 2, FileChanged: true}},
		"escape":                 {map[string]string{"live/dev/main.tf": call(`"../../../outside"`)}, false, nil},
		"unknown literal":        {map[string]string{"live/dev/main.tf": call(`"network"`)}, false, nil},
		"absolute":               {map[string]string{"live/dev/main.tf": call(`"/srv/modules/network"`)}, false, nil},
		"drive letter":           {map[string]string{"live/dev/main.tf": call(`"C:\\modules\\network"`)}, false, nil},
		"empty":                  {map[string]string{"live/dev/main.tf": call(`""`)}, false, nil},
		"non-literal":            {map[string]string{"live/dev/main.tf": call(`"${var.dir}/x"`)}, false, nil},
		"no source":              {map[string]string{"live/dev/main.tf": "module \"x\" {\n  cidr = 1\n}\n"}, false, nil},
		"example target":         {map[string]string{"examples/complete/main.tf": "\n", "live/dev/main.tf": call(`"../../examples/complete"`)}, false, nil},
		"tftest file":            {map[string]string{"tests/basic.tftest.hcl": "run \"x\" {}\n"}, false, nil},
		"tftest json file":       {map[string]string{"tests/basic.tftest.json": "{}\n"}, false, nil},
		"json config":            {map[string]string{"live/dev/main.tf.json": "{}\n"}, false, nil},
		"parse error":            {map[string]string{"live/dev/main.tf": "module \"x\" {\n"}, false, nil},
		"link":                   {map[string]string{"live/dev/link.md": "->../prod/main.tf"}, false, nil},
		"dot terraform excluded": {map[string]string{"live/prod/.terraform/modules/m/main.tf": call(`"lib/x"`)}, true, nil},
		"wrappers caller counted": {map[string]string{"wrappers/network/main.tf": call(`"../../infra/modules/network"`)}, true,
			&ModuleCaller{Area: ".", File: "wrappers/network/main.tf", Line: 2, FileChanged: true}},
		"tests caller counted": {map[string]string{"tests/fixture/main.tf": call(`"../../infra/modules/network"`)}, true,
			&ModuleCaller{Area: ".", File: "tests/fixture/main.tf", Line: 2, FileChanged: true}},
	} {
		t.Run(name, func(t *testing.T) {
			remotes := []string{
				`"terraform-aws-modules/vpc/aws"`, `"app.terraform.io/acme/vpc/aws"`, `"git::https://example.com/vpc.git"`,
				`"github.com/acme/vpc"`, `"bitbucket.org/acme/vpc"`, `"s3::https://s3.amazonaws.com/b/vpc.zip"`,
				`"gcs::https://www.googleapis.com/storage/v1/b/vpc.zip"`, `"https://example.com/vpc.zip"`,
				`"http://example.com/vpc.zip"`, `"hg::http://example.com/vpc.hg"`,
			}
			head := monoHead()
			for i, src := range remotes {
				head["live/remote/r"+string(rune('a'+i))+".tf"] = call(src)
			}
			maps.Copy(head, c.extra)
			r := runMono(t, head, monoBase(), testProfile, false)
			m := callersOf(r, netModule)
			if m == nil {
				t.Fatal("no entry for the network module")
			}
			if m.Complete != c.complete {
				t.Errorf("complete %v, want %v", m.Complete, c.complete)
			}
			if c.caller != nil && !slices.Contains(m.Callers, *c.caller) {
				t.Errorf("callers %+v, want one %+v", m.Callers, *c.caller)
			}
		})
	}
}

func TestModuleCallersRootTarget(t *testing.T) {
	base := map[string]string{"main.tf": "variable \"a\" {}\n", "wrappers/main.tf": "module \"w\" {\n  source = \"../\"\n}\n"}
	head := withFiles(base, "main.tf", "variable \"b\" {}\n")
	r := runMono(t, head, base, testProfile, false)
	m := callersOf(r, ".")
	if m == nil || !m.Complete {
		t.Fatalf("root entry %+v", m)
	}
	want := []ModuleCaller{{Area: ".", File: "wrappers/main.tf", Line: 2, FileChanged: false}}
	if !reflect.DeepEqual(m.Callers, want) {
		t.Errorf("callers %+v, want %+v", m.Callers, want)
	}
}

// TestModuleCallersAbsentAtHead checks a module the change removes gets no
// entry.
func TestModuleCallersAbsentAtHead(t *testing.T) {
	head := withFiles(monoHead(), "infra/modules/network/variables.tf", "")
	r := runMono(t, head, monoBase(), testProfile, false)
	if m := callersOf(r, netModule); m != nil {
		t.Errorf("entry for a removed module: %+v", *m)
	}
	if !slices.Contains(r.Scope.ModuleAreas, netModule) {
		t.Errorf("module_areas %q lacks the removed module, which base and head share", r.Scope.ModuleAreas)
	}
}

func TestModuleCallersUnreadableDir(t *testing.T) {
	head := writeTree(t, withFiles(monoHead(), "live/locked/x.md", "x"))
	locked := filepath.Join(head, "live", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	r, err := run(Options{Head: head, Base: writeTree(t, monoBase()), Limits: DefaultLimits}, Rules)
	if err != nil {
		t.Fatal(err)
	}
	if m := callersOf(r, netModule); m == nil || m.Complete {
		t.Errorf("entry %+v, want complete false with an unreadable directory", m)
	}
}

func TestModuleCallersFileChangedByDiff(t *testing.T) {
	head := writeTree(t, monoHead())
	// The diff lists the network module and prod only: stage is the head's.
	diff := "diff --git a/infra/modules/network/variables.tf b/infra/modules/network/variables.tf\n" +
		"--- a/infra/modules/network/variables.tf\n+++ b/infra/modules/network/variables.tf\n" +
		"@@ -2,6 +2,2 @@\n   type = string\n }\n-\n-variable \"name\" {\n-  type = string\n-}\n" +
		"diff --git a/live/prod/main.tf b/live/prod/main.tf\n--- a/live/prod/main.tf\n+++ b/live/prod/main.tf\n" +
		"@@ -3,3 +3,2 @@\n   cidr   = \"10.0.0.0/16\"\n-  name   = \"a\"\n }\n"
	df := filepath.Join(t.TempDir(), "change.diff")
	if err := os.WriteFile(df, []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run(Options{Head: head, Diff: df, Limits: DefaultLimits}, Rules)
	if err != nil {
		t.Fatal(err)
	}
	m := callersOf(r, netModule)
	if m == nil {
		t.Fatal("no entry")
	}
	got := map[string]bool{}
	for _, c := range m.Callers {
		got[c.File] = c.FileChanged
	}
	if !got["live/prod/main.tf"] || got["live/stage/main.tf"] {
		t.Errorf("file_changed %v, want prod true and stage false", got)
	}
}

func TestModuleCallersCap(t *testing.T) {
	var b strings.Builder
	for i := range maxModuleCallers + 1 {
		b.WriteString("module \"n" + strconv.Itoa(i) + "\" {\n  source = \"../../infra/modules/network\"\n}\n")
	}
	head := withFiles(monoHead(), "live/many/main.tf", b.String())
	_, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, monoBase()), Limits: DefaultLimits}, Rules)
	if !errors.Is(err, ErrCallerLimit) {
		t.Errorf("error %v, want ErrCallerLimit", err)
	}
}

func downgradeIDs() []string {
	return []string{"compat.default-changed", "compat.generated-policy-narrowed", "compat.type-narrowed", "compat.variable-now-required", "compat.variable-removed"}
}

// TestModuleCallersTerragruntBlocksDowngrade checks an unchanged
// terragrunt.hcl beside a changed .tf caller makes the scan incomplete, so
// no downgrade applies.
func TestModuleCallersTerragruntBlocksDowngrade(t *testing.T) {
	tg := "terraform {\n  source = \"../../infra/modules/network\"\n}\n"
	head := withFiles(monoHead(), "live/stage/terragrunt.hcl", tg)
	base := withFiles(monoBase(), "live/stage/terragrunt.hcl", tg)
	r := runMono(t, head, base, writeProfile(t, "format: 1", "consumers: in-repo", "partitions: portable", "examples: required", "release: registry"), false)
	if m := callersOf(r, netModule); m == nil || m.Complete {
		t.Errorf("entry %+v, want complete false", m)
	}
	if len(r.InRepoDowngrade) != 0 {
		t.Errorf("in_repo_downgrade %q, want []", r.InRepoDowngrade)
	}
}
