package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const interfaceDiffRoot = "../../testdata/interface-diff"

var (
	testHeadSHA = strings.Repeat("a", 40)
	testBaseSHA = strings.Repeat("b", 40)
)

// runInterfaceDiff runs the trees under dir, with a base when dir has one,
// and both commits.
func runInterfaceDiff(t *testing.T, dir string) *Report {
	t.Helper()
	o := Options{Head: filepath.Join(dir, "head"), HeadSHA: testHeadSHA, MergeBaseSHA: testBaseSHA, Limits: DefaultLimits,
		Version: "test", BinarySHA256: strings.Repeat("0", 64)}
	if st, err := os.Stat(filepath.Join(dir, "base")); err == nil && st.IsDir() {
		o.Base = filepath.Join(dir, "base")
	}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// diffKeys lists every element of a diff as "<area> <section> <key> <change>",
// and a changed required_version as "<area> required_version changed".
func diffKeys(d *InterfaceDiff) []string {
	var out []string
	for _, a := range d.Areas {
		if a.RequiredVersion != nil {
			out = append(out, a.Path+" required_version changed")
		}
		for _, s := range []struct {
			name string
			keys [][2]string
		}{
			{"variables", elemKeys(a.Variables)},
			{"outputs", elemKeys(a.Outputs)},
			{"providers", elemKeys(a.Providers)},
			{"resources", elemKeys(a.Resources)},
			{"moved", elemKeys(a.Moved)},
			{"module_calls", elemKeys(a.ModuleCalls)},
		} {
			for _, k := range s.keys {
				out = append(out, strings.Join([]string{a.Path, s.name, k[0], k[1]}, " "))
			}
		}
	}
	slices.Sort(out)
	return out
}

func elemKeys[V any](cs []elemChange[V]) [][2]string {
	var out [][2]string
	for _, c := range cs {
		out = append(out, [2]string{c.key, c.change})
	}
	return out
}

func readKeys(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for l := range strings.SplitSeq(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

// diffDoc returns the report's interface diff as decoded JSON, after it is
// validated against its schema and the lint schema.
func diffDoc(t *testing.T, set schemaSet, r *Report) map[string]any {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	v := decodeJSON(t, data)
	if errs := set.validate("lint.schema.json", v); len(errs) > 0 {
		t.Errorf("lint schema: %v", errs)
	}
	doc, _ := v.(map[string]any)["interface_diff"].(map[string]any)
	if doc == nil {
		t.Fatal("no interface_diff in the report")
	}
	if errs := set.validate(interfaceDiffID, doc); len(errs) > 0 {
		t.Errorf("interface diff schema: %v\n%s", errs, data)
	}
	return doc
}

const interfaceDiffID = "https://github.com/modulestf/facts/schema/interface-diff.schema.json"

func TestBuildInterfaceDiffAllSections(t *testing.T) {
	set := loadSchemas(t)
	dir := filepath.Join(interfaceDiffRoot, "all-sections")
	r := runInterfaceDiff(t, dir)
	diffDoc(t, set, r)
	got, want := diffKeys(r.InterfaceDiff), readKeys(t, filepath.Join(dir, "expected.txt"))
	if !slices.Equal(got, want) {
		t.Errorf("elements:\n got %q\nwant %q", got, want)
	}
	root := r.InterfaceDiff.Areas[0]
	if root.Path != "." || root.Kind != AreaRoot {
		t.Fatalf("first area %s %s, want the root", root.Path, root.Kind)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"required_version base", *root.RequiredVersion.Base, ">= 1.5.7"},
		{"required_version head", *root.RequiredVersion.Head, ">= 1.9.0"},
		{"name nullable at base", root.Variables[0].base.Nullable, true},
		{"name nullable at head", root.Variables[0].head.Nullable, false},
		{"name validation", root.Variables[0].head.Validation, []validationValue{{Condition: "length(var.name) <= 64", ErrorMessage: `"Name is too long."`}}},
		{"name description", *root.Variables[0].head.Description, `"Name of the thing"`},
		{"token sensitive", root.Variables[1].head.Sensitive, true},
		{"token default", *root.Variables[1].head.Default, "null"},
		{"output sensitive", root.Outputs[0].head.Sensitive, true},
		{"aws aliases", root.Providers[0].head.ConfigurationAliases, []string{"aws.replica"}},
		{"aws constraint", *root.Providers[0].head.VersionConstraint, ">= 6.0"},
		{"random implied source", root.Providers[2].head.Source, "hashicorp/random"},
		{"resource provider", *root.Resources[0].head.Prov, "aws.replica"},
		{"moved to", root.Moved[0].head.To, "aws_demo_thing.this[0]"},
		{"module version", *root.ModuleCalls[0].head.Version, "~> 2.0"},
		{"module source", root.ModuleCalls[0].head.Source, "terraform-aws-modules/demo/aws"},
	}
	for _, c := range checks {
		if g, w := mustJSON(t, c.got), mustJSON(t, c.want); string(g) != string(w) {
			t.Errorf("%s: got %s, want %s", c.name, g, w)
		}
	}
}

// TestInterfaceDiffSchemaMutations changes one part of a valid document per
// section and checks the schema refuses it, so each section is validated.
func TestInterfaceDiffSchemaMutations(t *testing.T) {
	set := loadSchemas(t)
	r := runInterfaceDiff(t, filepath.Join(interfaceDiffRoot, "all-sections"))
	data, err := json.Marshal(r.InterfaceDiff)
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() map[string]any {
		doc := decodeJSON(t, data).(map[string]any)
		if errs := set.validate(interfaceDiffID, doc); len(errs) > 0 {
			t.Fatalf("unmutated document: %v", errs)
		}
		return doc
	}
	root := func(doc map[string]any) map[string]any { return doc["areas"].([]any)[0].(map[string]any) }
	first := func(doc map[string]any, section string) map[string]any {
		return root(doc)[section].([]any)[0].(map[string]any)
	}
	type mutation struct {
		name   string
		mutate func(doc map[string]any)
	}
	muts := []mutation{
		{"head sha", func(d map[string]any) { d["head_sha"] = "HEAD" }},
		{"area kind", func(d map[string]any) { root(d)["kind"] = "wrapper" }},
		{"area path", func(d map[string]any) { root(d)["path"] = "../x" }},
		{"required_version both null", func(d map[string]any) {
			root(d)["required_version"] = map[string]any{"base": nil, "head": nil}
		}},
		{"required_version extra key", func(d map[string]any) {
			root(d)["required_version"].(map[string]any)["x"] = "1"
		}},
	}
	for _, s := range []string{"variables", "outputs", "providers", "resources", "moved", "module_calls"} {
		muts = append(muts,
			mutation{s + " missing section", func(d map[string]any) { delete(root(d), s) }},
			mutation{s + " added with a base", func(d map[string]any) {
				e := first(d, s)
				e["change"] = "added"
				if e["base"] == nil {
					e["base"], e["head"] = e["head"], nil
				}
			}},
			mutation{s + " removed with a head", func(d map[string]any) {
				e := first(d, s)
				e["change"] = "removed"
				if e["head"] == nil {
					e["base"], e["head"] = nil, e["base"]
				}
			}},
			mutation{s + " unknown change", func(d map[string]any) { first(d, s)["change"] = "renamed" }},
			mutation{s + " value extra key", func(d map[string]any) {
				e := first(d, s)
				v, _ := e["head"].(map[string]any)
				if v == nil {
					v = e["base"].(map[string]any)
				}
				v["extra"] = true
			}},
			mutation{s + " value missing key", func(d map[string]any) {
				e := first(d, s)
				v, _ := e["head"].(map[string]any)
				if v == nil {
					v = e["base"].(map[string]any)
				}
				for k := range v {
					delete(v, k)
					break
				}
			}},
		)
	}
	muts = append(muts,
		mutation{"resource address", func(d map[string]any) { first(d, "resources")["address"] = "Not An Address" }},
		mutation{"variable nullable type", func(d map[string]any) {
			first(d, "variables")["head"].(map[string]any)["nullable"] = "false"
		}},
		mutation{"provider empty source", func(d map[string]any) {
			first(d, "providers")["head"].(map[string]any)["source"] = ""
		}},
	)
	for _, m := range muts {
		t.Run(m.name, func(t *testing.T) {
			doc := fresh()
			m.mutate(doc)
			if errs := set.validate(interfaceDiffID, doc); len(errs) == 0 {
				t.Error("mutated document accepted")
			}
		})
	}
}

// TestInterfaceDiffOracle validates the interface diff of every oracle
// fixture and compares the compat fixtures with their hand-written lists.
func TestInterfaceDiffOracle(t *testing.T) {
	set := loadSchemas(t)
	compat := 0
	for _, src := range oracleFixtures(t) {
		name := filepath.Base(src)
		t.Run(name, func(t *testing.T) {
			dir := setupOracle(t, src)
			r := runInterfaceDiff(t, dir)
			if _, err := os.Stat(filepath.Join(dir, "base")); err != nil {
				if r.InterfaceDiff != nil {
					t.Error("interface diff with no base tree")
				}
				return
			}
			diffDoc(t, set, r)
			if !strings.HasPrefix(name, "compat-") {
				return
			}
			compat++
			want := readKeys(t, filepath.Join(interfaceDiffRoot, name+".txt"))
			if got := diffKeys(r.InterfaceDiff); !slices.Equal(got, want) {
				t.Errorf("elements:\n got %q\nwant %q", got, want)
			}
		})
	}
	if compat == 0 {
		t.Error("no compat fixture")
	}
}

func TestBuildInterfaceDiffNeedsBaseAndCommits(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": "variable \"x\" {}\n"})
	base := writeTree(t, map[string]string{"main.tf": ""})
	for _, c := range []struct {
		name string
		o    Options
		want bool
	}{
		{"no base", Options{Head: head, HeadSHA: testHeadSHA, MergeBaseSHA: testBaseSHA}, false},
		{"no head commit", Options{Head: head, Base: base, MergeBaseSHA: testBaseSHA}, false},
		{"no merge-base commit", Options{Head: head, Base: base, HeadSHA: testHeadSHA}, false},
		{"all inputs", Options{Head: head, Base: base, HeadSHA: testHeadSHA, MergeBaseSHA: testBaseSHA}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.o.Limits = DefaultLimits
			r, err := Run(c.o)
			if err != nil {
				t.Fatal(err)
			}
			if (r.InterfaceDiff != nil) != c.want {
				t.Errorf("interface diff present %v, want %v", r.InterfaceDiff != nil, c.want)
			}
		})
	}
}

func TestBuildInterfaceDiffUnchanged(t *testing.T) {
	tests := []struct {
		name       string
		base, head string
	}{
		{"body edit only", "resource \"a_b\" \"c\" {\n  x = 1\n}\n", "resource \"a_b\" \"c\" {\n  x = 2\n}\n"},
		{"implied source made explicit", "terraform {\n  required_providers {\n    aws = {}\n  }\n}\n",
			"terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n    }\n  }\n}\n"},
		{"default nullable written", "variable \"x\" {}\n", "variable \"x\" {\n  nullable = true\n}\n"},
		{"moved between files", "variable \"x\" {}\n", "\n\nvariable \"x\" {}\n"},
		{"whitespace in a default", "variable \"x\" {\n  default = {a=1}\n}\n", "variable \"x\" {\n  default = { a = 1 }\n}\n"},
		{"whitespace in a type", "variable \"x\" {\n  type = list(string)\n}\n", "variable \"x\" {\n  type = list( string )\n}\n"},
		{"comment in an output value", "output \"o\" {\n  value = [1, 2]\n}\n", "output \"o\" {\n  value = [\n    1, # one\n    2\n  ]\n}\n"},
		{"whitespace in a moved from", "moved {\n  from = a_b.c[0]\n  to   = a_b.d\n}\n", "moved {\n  from = a_b.c[ 0 ]\n  to   = a_b.d\n}\n"},
		{"whitespace in a moved to", "moved {\n  from = a_b.c\n  to   = a_b.d[0]\n}\n", "moved {\n  from = a_b.c\n  to   = a_b.d[ 0 ]\n}\n"},
		{"whitespace in a count", "resource \"a_b\" \"c\" {\n  count = var.n>0?1:0\n}\n", "resource \"a_b\" \"c\" {\n  count = var.n > 0 ? 1 : 0\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runInterfaceDiff(t, writeTree(t, map[string]string{"base/main.tf": tt.base, "head/main.tf": tt.head}))
			if r.InterfaceDiff == nil || len(r.InterfaceDiff.Areas) != 0 {
				t.Errorf("want no area, got %+v", r.InterfaceDiff)
			}
		})
	}
}

func TestBuildInterfaceDiffChanged(t *testing.T) {
	tf := func(versions ...string) string {
		var b strings.Builder
		for _, v := range versions {
			b.WriteString("terraform {\n  required_version = \"" + v + "\"\n}\n")
		}
		return b.String()
	}
	tests := []struct {
		name       string
		base, head string
		want       []string
	}{
		{"second required_version block", tf(">= 1.5.7"), tf(">= 1.5.7", ">= 1.9"), []string{". required_version changed"}},
		{"string text inside quotes", "variable \"x\" {\n  default = \"a b\"\n}\n", "variable \"x\" {\n  default = \"a  b\"\n}\n",
			[]string{". variables x changed"}},
		{"moved to another index", "moved {\n  from = a_b.c[0]\n  to   = a_b.d\n}\n", "moved {\n  from = a_b.c[ 0 ]\n  to   = a_b.e\n}\n",
			[]string{". moved a_b.c[0] changed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runInterfaceDiff(t, writeTree(t, map[string]string{"base/main.tf": tt.base, "head/main.tf": tt.head}))
			if got := diffKeys(r.InterfaceDiff); !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
	r := runInterfaceDiff(t, writeTree(t, map[string]string{"base/main.tf": tf(">= 1.5.7"), "head/main.tf": tf(">= 1.5.7", ">= 1.9")}))
	rv := r.InterfaceDiff.Areas[0].RequiredVersion
	if *rv.Base != ">= 1.5.7" || *rv.Head != ">= 1.5.7, >= 1.9" {
		t.Errorf("required_version %q -> %q", *rv.Base, *rv.Head)
	}
}

func TestNormExpr(t *testing.T) {
	tests := []struct{ a, b string }{
		{"{a=1}", "{ a = 1 }"},
		{"list(string)", "list( string )"},
		{"[\n  1, # one\n  2,\n]", "[ 1, 2, ]"},
	}
	for _, tt := range tests {
		if normExpr(tt.a) != normExpr(tt.b) {
			t.Errorf("%q and %q differ: %q, %q", tt.a, tt.b, normExpr(tt.a), normExpr(tt.b))
		}
	}
	for _, d := range [][2]string{{`"a b"`, `"a  b"`}, {"var.a", "var.b"}, {"x", "x.y"}} {
		if normExpr(d[0]) == normExpr(d[1]) {
			t.Errorf("%q and %q compare equal", d[0], d[1])
		}
	}
}
