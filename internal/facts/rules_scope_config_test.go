package facts

import (
	"strings"
	"testing"
)

func runScopeConfig(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	o := Options{Head: writeTree(t, head), ProfileDir: testProfile, Limits: DefaultLimits}
	if base != nil {
		o.Base = writeTree(t, base)
	}
	r, err := run(o, scopeConfigRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestScopeConfigRuleShape(t *testing.T) {
	for _, r := range scopeConfigRules {
		if !strings.HasPrefix(r.ID, "scope.") || !r.NeedsBase || r.Tier != "candidate" {
			t.Errorf("%s: tier %s, needs base %v", r.ID, r.Tier, r.NeedsBase)
		}
	}
}

func TestScopeConfigNoBase(t *testing.T) {
	r := runScopeConfig(t, nil, map[string]string{"main.tf": "resource \"random_id\" \"x\" {}\n", "modules/m/main.tf": ""})
	for _, e := range r.Manifest {
		if e.Status != StatusNotApplicable && e.ReasonCode != ReasonNoBase {
			t.Errorf("%s in %s: %s %s", e.RuleID, e.Area, e.Status, e.ReasonCode)
		}
	}
}

// An empty base is a new module: scope.new-module, an llm rule, replaces
// scope.provider and scope.new-submodule, and scope.module-call still runs.
func TestScopeConfigNewModule(t *testing.T) {
	r := runScopeConfig(t, map[string]string{}, map[string]string{
		"main.tf":           "resource \"random_id\" \"x\" {}\nmodule \"r\" {\n  source = \"example-org/r/aws\"\n}\n",
		"modules/m/main.tf": "resource \"aws_demo_thing\" \"x\" {}\n",
	})
	for rule, want := range map[string]string{"scope.provider": "clean", "scope.new-submodule": "clean", "scope.module-call": "candidate"} {
		area := "."
		if rule == "scope.new-submodule" {
			area = "modules/m"
		}
		if s := statusOf(r.Manifest, rule, area); s != want {
			t.Errorf("%s: %s", rule, s)
		}
	}
}

// A local source through a symbolic link, or to a directory the head lacks,
// is a candidate; a base docs/SCOPE.md that is a link is not read.
func TestScopeConfigLinks(t *testing.T) {
	base := map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {}\n", "modules/real/main.tf": ""}
	head := map[string]string{
		"main.tf":              "resource \"aws_demo_widget\" \"this\" {}\nmodule \"a\" {\n  source = \"./modules/alias\"\n}\nmodule \"b\" {\n  source = \"./modules/missing\"\n}\n",
		"modules/real/main.tf": "",
		"modules/alias":        "->real",
	}
	r := runScopeConfig(t, base, head)
	var got []string
	for _, c := range candidatesOf(r, "scope.module-call") {
		got = append(got, c.Subject)
	}
	if strings.Join(got, " ") != ".:module.a .:module.b" {
		t.Errorf("candidates %v", got)
	}
	base["docs/SCOPE.md"] = "->../main.tf"
	head["modules/new/main.tf"] = "resource \"aws_demo_new\" \"x\" {}\n"
	r = runScopeConfig(t, base, head)
	for _, rule := range []string{"scope.provider", "scope.new-submodule"} {
		area := "."
		if rule == "scope.new-submodule" {
			area = "modules/new"
		}
		if s := statusOf(r.Manifest, rule, area); !strings.HasPrefix(s, "not_evaluated") {
			t.Errorf("%s: %s", rule, s)
		}
	}
}

// A provider is reported once, at its first added site in file order, in
// the area of that site, even when two areas add it.
func TestScopeProviderOncePerSource(t *testing.T) {
	base := map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {}\n", "modules/b/main.tf": "resource \"aws_demo_b\" \"this\" {}\n"}
	head := map[string]string{
		"main.tf":           "resource \"aws_demo_widget\" \"this\" {}\nresource \"helm_release\" \"x\" {}\n",
		"modules/b/main.tf": "resource \"aws_demo_b\" \"this\" {}\nresource \"helm_release\" \"y\" {}\n",
	}
	cs := candidatesOf(runScopeConfig(t, base, head), "scope.provider")
	if len(cs) != 1 || cs[0].File != "main.tf" || *cs[0].Line != 2 || len(cs[0].Evidence.SourceLines) != 2 {
		t.Errorf("candidates %+v", cs)
	}
}

// A base provider block alone is no base provider, so a resource of that
// provider the change adds is a candidate.
func TestScopeProviderIgnoresBaseProviderBlock(t *testing.T) {
	base := map[string]string{"main.tf": "provider \"google\" {}\nresource \"aws_demo_widget\" \"this\" {}\n"}
	head := map[string]string{"main.tf": "provider \"google\" {}\nresource \"aws_demo_widget\" \"this\" {}\nresource \"google_x\" \"y\" {}\n"}
	cs := candidatesOf(runScopeConfig(t, base, head), "scope.provider")
	if len(cs) != 1 || cs[0].Subject != `.:provider["registry.terraform.io/hashicorp/google"]` || *cs[0].Line != 3 {
		t.Errorf("candidates %+v", cs)
	}
}

// The interim not_evaluated for a provider added only to an existing
// required_providers drops the candidate of another new provider too.
func TestScopeProviderInterimDropsOthers(t *testing.T) {
	tf := func(extra string) string {
		return "terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n    }\n" + extra + "  }\n}\n"
	}
	base := map[string]string{"versions.tf": tf(""), "main.tf": "resource \"aws_demo_widget\" \"this\" {}\n"}
	head := map[string]string{
		"versions.tf": tf("    helm = {\n      source = \"hashicorp/helm\"\n    }\n"),
		"main.tf":     "resource \"aws_demo_widget\" \"this\" {}\nresource \"random_id\" \"x\" {}\n",
	}
	r := runScopeConfig(t, base, head)
	if s := statusOf(r.Manifest, "scope.provider", "."); !strings.HasPrefix(s, "not_evaluated") || len(candidatesOf(r, "scope.provider")) != 0 {
		t.Errorf("%s %+v", s, candidatesOf(r, "scope.provider"))
	}
	delete(head, "versions.tf")
	head["versions.tf"] = tf("")
	if cs := candidatesOf(runScopeConfig(t, base, head), "scope.provider"); len(cs) != 1 {
		t.Errorf("without the interim case: %+v", cs)
	}
}
