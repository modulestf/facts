package facts

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// monorepo is a layout outside terraform-aws-modules: modules under infra/
// called from live roots, a nested module, and directories discovery skips.
func monorepo() map[string]string {
	return map[string]string{
		"infra/modules/network/main.tf":         "resource \"aws_vpc\" \"this\" {}\n",
		"infra/modules/network/variables.tf":    "variable \"cidr\" {}\n",
		"infra/modules/network/README.md":       "",
		"infra/modules/network/sub/main.tf":     "",
		"infra/live/prod/main.tf":               "module \"net\" {\n  source = \"../../modules/network\"\n  cidr   = \"10.0.0.0/16\"\n}\n",
		"infra/modules/network/examples/a/m.tf": "",
		"infra/tests/t.tf":                      "",
		"wrappers/w/main.tf":                    "",
		".terraform/x/main.tf":                  "",
		"docs/usage.md":                         "",
	}
}

func TestDiscoverModules(t *testing.T) {
	head := mustTree(t, writeTree(t, monorepo()))
	mods, err := discoverModules(head, nil, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for d := range mods {
		got = append(got, d)
	}
	slices.Sort(got)
	want := []string{"infra/live/prod", "infra/modules/network", "infra/modules/network/sub"}
	if !slices.Equal(got, want) {
		t.Errorf("discovered %v, want %v", got, want)
	}
}

// A top-level modules/ or examples/ stays Classify's, even with a loose .tf
// file, and a directory only holding non-configuration files is no area.
func TestDiscoverModulesLeavesClassifyAreas(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "", "modules/a/main.tf": "", "modules/a/sub/x.tf": "", "modules/x.tf": "",
		"examples/e/main.tf": "", "examples/y.tf": "", "scripts/run.sh": "",
	}))
	mods, err := discoverModules(head, nil, DefaultLimits)
	if err != nil || len(mods) != 0 {
		t.Errorf("discovered %v, err %v; want none", mods, err)
	}
}

func TestOwnerOf(t *testing.T) {
	mods := map[string]bool{"infra/modules/network": true, "infra/modules/network/sub": true, "infra/link": true}
	isMod := func(p string) bool { return mods[p] }
	cases := map[string]string{
		"main.tf":                                  ". root",
		"modules/child/main.tf":                    "modules/child submodule",
		"examples/e/main.tf":                       "examples/e example",
		"examples/README.md":                       ". root",
		"tests/a.tf":                               ". root",
		"wrappers/a/main.tf":                       ". root",
		".github/workflows/ci.yml":                 ". root",
		"infra/modules/network/main.tf":            "infra/modules/network module",
		"infra/modules/network/templates/a.sh":     "infra/modules/network module",
		"infra/modules/network/sub/main.tf":        "infra/modules/network/sub module",
		"infra/modules/network/subway/main.tf":     "infra/modules/network module", // segment-bounded
		"infra/modules/network/examples/a/m.tf":    ". root",                       // ignored segment
		"infra/modules/network/tests/t.tf":         ". root",
		"infra/modules/network/.hidden/x.tf":       ". root",
		"infra/link":                               "infra/link module", // an Issue area owns its own path
		"infra/other/main.tf":                      ". root",
		"infra/modules/other/main.tf":              ". root",
		"infra/modules/network/sub/deeper/file.md": "infra/modules/network/sub module",
	}
	for p, want := range cases {
		a, k := ownerOf(p, isMod)
		if got := a + " " + k; got != want {
			t.Errorf("%s: got %q, want %q", p, got, want)
		}
	}
}

func TestFindAreasMonorepoNoBase(t *testing.T) {
	head := mustTree(t, writeTree(t, monorepo()))
	got := areaList(FindAreas(head, nil, DefaultLimits, true))
	want := strings.Join([]string{
		`.:root:false:true:false:""`,
		`infra/live/prod:module:false:true:false:""`,
		`infra/modules/network:module:false:true:false:""`,
		`infra/modules/network/sub:module:false:true:false:""`,
	}, " ")
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// A change under a module puts that module in scope, not the root; a file of
// any extension counts, and files under an ignored segment stay the root's.
func TestFindAreasMonorepoChange(t *testing.T) {
	baseFiles := monorepo()
	headFiles := monorepo()
	headFiles["infra/modules/network/main.tf"] = "resource \"aws_vpc\" \"this\" {\n  cidr_block = var.cidr\n}\n"
	headFiles["infra/modules/network/README.md"] = "changed"
	base, head := mustTree(t, writeTree(t, baseFiles)), mustTree(t, writeTree(t, headFiles))
	got := areaList(FindAreas(head, base, DefaultLimits, true))
	want := strings.Join([]string{
		`.:root:true:true:true:""`,
		`infra/live/prod:module:true:true:true:""`,
		`infra/modules/network:module:false:true:true:""`,
		`infra/modules/network/sub:module:true:true:true:""`,
	}, " ")
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	headFiles["infra/modules/network/examples/a/m.tf"] = "changed"
	head = mustTree(t, writeTree(t, headFiles))
	baseFiles["infra/modules/network/main.tf"] = headFiles["infra/modules/network/main.tf"]
	baseFiles["infra/modules/network/README.md"] = "changed"
	base = mustTree(t, writeTree(t, baseFiles))
	want = strings.Join([]string{
		`.:root:false:true:true:""`,
		`infra/live/prod:module:true:true:true:""`,
		`infra/modules/network:module:true:true:true:""`,
		`infra/modules/network/sub:module:true:true:true:""`,
	}, " ")
	if got := areaList(FindAreas(head, base, DefaultLimits, true)); got != want {
		t.Errorf("nested example change: got %s\nwant %s", got, want)
	}
}

func TestFindAreasDeletedModule(t *testing.T) {
	base := mustTree(t, writeTree(t, monorepo()))
	files := monorepo()
	delete(files, "infra/modules/network/sub/main.tf")
	head := mustTree(t, writeTree(t, files))
	got := areaList(FindAreas(head, base, DefaultLimits, true))
	if !strings.Contains(got, `infra/modules/network/sub:module:false:false:true:""`) {
		t.Errorf("deleted module is not an area in scope: %s", got)
	}
}

// A directory that held configuration at base and is a link at head stays an
// area, not evaluated; a link with no such history is not an area, and a
// changed link anywhere else leaves the root in scope exactly as before. A
// link named like configuration belongs to its directory.
func TestFindAreasOpaqueEntries(t *testing.T) {
	base := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "", "README.md": "", "infra/a/main.tf": "", "infra/a/variables.tf": "", "infra/old/main.tf": "",
	}))
	head := mustTree(t, writeTree(t, map[string]string{
		"main.tf": "", "README.md": "->/etc/hostname",
		"infra/a/main.tf": "->/etc/hostname", "infra/a/variables.tf": "",
		"infra/old": "->/tmp", // was a directory with .tf files at base
		"infra/new": "->/tmp", // never held configuration
		"modules/m": "->/tmp",
	}))
	as, err := FindAreas(head, base, DefaultLimits, true)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*Area{}
	for _, a := range as {
		by[a.Path] = a
	}
	if a := by["infra/old"]; a == nil || a.Kind != AreaModule || a.Issue == "" || a.Untouched {
		t.Errorf("infra/old: want an Issue area of kind module in scope, got %+v", a)
	}
	for _, p := range []string{"infra/new", "README.md"} {
		if by[p] != nil {
			t.Errorf("%s: a link with no configuration history became an area", p)
		}
	}
	if a := by["."]; a == nil || a.Untouched {
		t.Errorf("a changed root link must leave the root in scope, got %+v", a)
	}
	if a := by["infra/a"]; a == nil || a.Kind != AreaModule || a.Issue != "" || len(a.Head.Unsupported) == 0 {
		t.Errorf("infra/a: want a module area whose link-named main.tf is unsupported, got %+v", a)
	}
	if by["infra/a/main.tf"] != nil {
		t.Error("a link named like configuration became an area of its own")
	}
	if a := by["modules/m"]; a == nil || a.Kind != AreaSubmodule {
		t.Errorf("a link at modules/<m> must stay one submodule area, got %+v", a)
	}
}

// Many changed links never trip the area limit: only directories holding
// configuration are counted.
func TestChangedLinksDoNotCountAgainstLimit(t *testing.T) {
	baseFiles, headFiles := map[string]string{"main.tf": ""}, map[string]string{"main.tf": ""}
	for i := range 600 {
		headFiles["l"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+i/26))] = "->/tmp"
	}
	base, head := mustTree(t, writeTree(t, baseFiles)), mustTree(t, writeTree(t, headFiles))
	lim := DefaultLimits
	lim.MaxAreas = 2
	if _, err := FindAreas(head, base, lim, true); err != nil {
		t.Errorf("changed links counted against the area limit: %v", err)
	}
}

func TestDiscoverModulesLimit(t *testing.T) {
	files := map[string]string{}
	for _, d := range []string{"a", "b", "c"} {
		files["infra/"+d+"/main.tf"] = ""
	}
	head := mustTree(t, writeTree(t, files))
	lim := DefaultLimits
	lim.MaxAreas = 2
	if _, err := FindAreas(head, nil, lim, true); !errors.Is(err, ErrAreaLimit) {
		t.Errorf("err = %v, want ErrAreaLimit", err)
	}
	lim.MaxAreas = 3
	if _, err := FindAreas(head, nil, lim, true); err != nil {
		t.Errorf("at the limit: %v", err)
	}
	// Classify areas never count.
	cl := map[string]string{"main.tf": ""}
	for i := range 5 {
		cl["modules/m"+string(rune('a'+i))+"/main.tf"] = ""
		cl["examples/e"+string(rune('a'+i))+"/main.tf"] = ""
	}
	lim.MaxAreas = 1
	if _, err := FindAreas(mustTree(t, writeTree(t, cl)), nil, lim, true); err != nil {
		t.Errorf("classify areas counted against the limit: %v", err)
	}
}

// A module area takes exactly the rules in moduleRules, so a rule that lists
// the submodule kind must be classified here before it can run on a module.
func TestModuleRulesCoverEverySubmoduleRule(t *testing.T) {
	excluded := func(id string) bool {
		for _, p := range []string{"structure.", "profile.", "scope.", "examples."} {
			if strings.HasPrefix(id, p) {
				return true
			}
		}
		return false
	}
	for _, r := range Rules {
		if !slices.Contains(r.Kinds, AreaSubmodule) {
			continue
		}
		if moduleRules[r.ID] == excluded(r.ID) {
			t.Errorf("%s lists the submodule kind but is neither in moduleRules nor excluded by prefix", r.ID)
		}
		if got := kindApplies(r, AreaModule); got != moduleRules[r.ID] {
			t.Errorf("%s: kindApplies(module) = %v", r.ID, got)
		}
	}
	for id := range moduleRules {
		if !slices.ContainsFunc(Rules, func(r Rule) bool { return r.ID == id && slices.Contains(r.Kinds, AreaSubmodule) }) {
			t.Errorf("moduleRules lists %s, which is no rule of the submodule kind", id)
		}
	}
}

// End to end: a change to infra/modules/network is attributed to that area,
// the allowed rules judge it, and the live root that calls it is listed.
func TestRunMonorepoModuleChange(t *testing.T) {
	baseFiles := monorepo()
	headFiles := monorepo()
	headFiles["infra/modules/network/variables.tf"] = "variable \"cidr\" {}\nvariable \"region_name\" {}\n"
	r, err := run(Options{Head: writeTree(t, headFiles), Base: writeTree(t, baseFiles), Limits: DefaultLimits, ProfileDir: testProfile}, Rules)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, e := range r.Manifest {
		if e.Area == "infra/modules/network" {
			status[e.RuleID] = e.Status
		}
	}
	for id := range moduleRules {
		r, _ := ruleByID(id)
		if r.CrossArea {
			continue // filed under the root, not per area
		}
		if status[id] == "" || status[id] == StatusNotApplicable {
			t.Errorf("%s: manifest status %q for the changed module", id, status[id])
		}
	}
	if s := status["structure.variable-untyped"]; s != StatusNotApplicable {
		t.Errorf("structure.variable-untyped on a module: %q, want not_applicable", s)
	}
	if r.Callers == nil || len(*r.Callers) != 1 {
		t.Fatalf("callers = %+v, want one changed module", r.Callers)
	}
	cm := (*r.Callers)[0]
	if cm.Module != "infra/modules/network" || len(cm.Callers) != 1 || cm.Callers[0].Area != "infra/live/prod" {
		t.Errorf("callers = %+v, want infra/live/prod calling infra/modules/network", cm)
	}
	if r.Scope == nil || !slices.Contains(r.Scope.AreasCalling, "infra/live/prod") || !slices.Contains(r.Scope.AreasChecked, "infra/modules/network") {
		t.Errorf("scope = %+v", r.Scope)
	}
}

func ruleByID(id string) (Rule, bool) {
	for _, r := range Rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// A module source that leaves the tree, is absolute or has no area is no
// caller, and nothing outside the tree is read.
func TestCallersIgnoreUnresolvedSources(t *testing.T) {
	for _, src := range []string{"../../../../elsewhere", "/abs/path", "../../modules/missing", "git::https://example.com/x.git"} {
		files := monorepo()
		files["infra/live/prod/main.tf"] = "module \"net\" {\n  source = \"" + src + "\"\n}\n"
		headFiles := monorepo()
		headFiles["infra/live/prod/main.tf"] = files["infra/live/prod/main.tf"]
		headFiles["infra/modules/network/variables.tf"] = "variable \"cidr\" {}\nvariable \"x\" {}\n"
		r, err := run(Options{Head: writeTree(t, headFiles), Base: writeTree(t, files), Limits: DefaultLimits, ProfileDir: testProfile}, Rules)
		if err != nil {
			t.Fatal(err)
		}
		if r.Callers == nil || len(*r.Callers) != 1 || len((*r.Callers)[0].Callers) != 0 {
			t.Errorf("source %q: callers = %+v, want the changed module with no caller", src, r.Callers)
		}
	}
}
