package fixture

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// buildTD holds the builder's oracle cases. Each case directory has
// trigger.json, args.json (level, attribute, resource and the template
// files: a bare name is under templates/, a ./ name is in the case
// directory), want.json with the exit the first builder gave (0 built, 1
// refused, 2 failed), stdout.json with its output and, when built, tree/
// with every file it wrote. The ladder-pass-NN cases were captured from the
// skills repository's ladder pass test; the others from crafted triggers.
const buildTD = "../../testdata/fixture-build"

type buildArgs struct {
	Level            string `json:"level"`
	Attribute        string `json:"attribute"`
	Resource         string `json:"resource"`
	Template         string `json:"template"`
	ResourceTemplate string `json:"resource_template"`
}

func readTD(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func templatePath(dir, name string) string {
	if strings.HasPrefix(name, "./") {
		return filepath.Join(dir, name)
	}
	return filepath.Join(buildTD, "templates", name)
}

// caseOptions reads the build options of one oracle case.
func caseOptions(t *testing.T, dir string) BuildOptions {
	t.Helper()
	var a buildArgs
	if err := json.Unmarshal(readTD(t, filepath.Join(dir, "args.json")), &a); err != nil {
		t.Fatal(err)
	}
	o := BuildOptions{Level: a.Level, Attribute: a.Attribute, Resource: a.Resource,
		Trigger: readTD(t, filepath.Join(dir, "trigger.json")), Template: readTD(t, templatePath(dir, a.Template))}
	if a.ResourceTemplate != "" {
		o.ResourceTemplate = readTD(t, templatePath(dir, a.ResourceTemplate))
	}
	return o
}

// readTree reads every file under dir by its slash path.
func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := map[string][]byte{}
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out[p], err = r.ReadFile(p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func oracleCases(t *testing.T) []string {
	t.Helper()
	dirs := caseDirs(t, buildTD)
	if len(dirs) < 43 {
		t.Fatalf("%d oracle cases, want the 43 of the ladder pass test and more", len(dirs))
	}
	return dirs
}

// caseDirs lists the case directories under root, templates aside.
func caseDirs(t *testing.T, root string) []string {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range ents {
		if e.IsDir() && e.Name() != "templates" {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	return dirs
}

// TestBuildOracle builds every oracle case and compares the result and each
// file byte for byte with what the first builder printed and wrote.
func TestBuildOracle(t *testing.T) {
	for _, dir := range oracleCases(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) { checkCase(t, dir) })
	}
}

// diffTD holds the inputs on which this builder differs from the first one
// on purpose, in the oracle layout with this builder's result and
// old-builder.txt saying what the first builder did.
const diffTD = "../../testdata/fixture-build-differences"

// TestBuildDifferences builds each declared difference and compares it with
// this builder's recorded result.
func TestBuildDifferences(t *testing.T) {
	dirs := caseDirs(t, diffTD)
	if len(dirs) < 9 {
		t.Fatalf("%d difference cases, want 9", len(dirs))
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			if note, err := os.ReadFile(filepath.Join(dir, "old-builder.txt")); err != nil || len(note) == 0 {
				t.Fatalf("no old-builder.txt: %v", err)
			}
			checkCase(t, dir)
		})
	}
}

// checkCase builds one case directory and compares the result and each
// file byte for byte with the recorded ones.
func checkCase(t *testing.T, dir string) {
	t.Helper()
	var want struct{ Exit int }
	if err := json.Unmarshal(readTD(t, filepath.Join(dir, "want.json")), &want); err != nil {
		t.Fatal(err)
	}
	b, err := Build(caseOptions(t, dir))
	if want.Exit == 2 {
		if err == nil {
			t.Fatalf("built %v, want an error", b.Result)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	out, err := EncodeResult(b.Result)
	if err != nil {
		t.Fatal(err)
	}
	if wantOut := readTD(t, filepath.Join(dir, "stdout.json")); !bytes.Equal(append(out, '\n'), wantOut) {
		t.Errorf("stdout\n got %s\nwant %s", out, wantOut)
	}
	if b.OK != (want.Exit == 0) {
		t.Fatalf("ok %v, want exit %d", b.OK, want.Exit)
	}
	if !b.OK {
		if len(b.Files) != 0 {
			t.Errorf("a refusal has %d files", len(b.Files))
		}
		return
	}
	wantTree := readTree(t, filepath.Join(dir, "tree"))
	got := map[string][]byte{}
	for _, f := range b.Files {
		if _, dup := got[f.Path]; dup {
			t.Errorf("%s twice", f.Path)
		}
		got[f.Path] = f.Data
	}
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for k := range wantTree {
			if !yield(k) {
				return
			}
		}
	}) {
		if g, ok := got[p]; !ok {
			t.Errorf("%s not built", p)
		} else if !bytes.Equal(g, wantTree[p]) {
			t.Errorf("%s\n got %q\nwant %q", p, g, wantTree[p])
		}
	}
	for p := range got {
		if _, ok := wantTree[p]; !ok {
			t.Errorf("%s built, not in the oracle", p)
		}
	}
}

// TestWriteFiles writes a build and reads it back, and refuses an --out
// that is not an empty directory or is a link.
func TestWriteFiles(t *testing.T) {
	dir := filepath.Join(buildTD, "ladder-pass-01")
	b, err := Build(caseOptions(t, dir))
	if err != nil || !b.OK {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := WriteFiles(out, b.Files); err != nil {
		t.Fatal(err)
	}
	got, want := readTree(t, out), readTree(t, filepath.Join(dir, "tree"))
	if len(got) != len(want) {
		t.Fatalf("%d files written, want %d", len(got), len(want))
	}
	for p, w := range want {
		if !bytes.Equal(got[p], w) {
			t.Errorf("%s differs", p)
		}
	}
	if err := WriteFiles(out, b.Files); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a full directory: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFiles(link, b.Files); err == nil {
		t.Error("a link was written through")
	}
	if err := WriteFiles(filepath.Join(t.TempDir(), "none"), b.Files); err == nil {
		t.Error("a missing directory was made")
	}
	// A path that climbs out is refused by the root, whatever the builder.
	if err := WriteFiles(t.TempDir(), []File{{Path: "../x/escape", Data: nil}}); err == nil {
		t.Error("a path out of the root was written")
	}
}

// TestRenameRefs pins how the expression's references become locals: only
// a run of the expression's own tokens, so the text of a quoted string, a
// heredoc or a comment stays as it is while an interpolation is renamed.
// The first builder replaced text, inside strings too, which changed the
// value a literal holds; that is the one place the two differ.
func TestRenameRefs(t *testing.T) {
	rs := []rename{
		{from: "var.ab", to: "local.ladder_in_1"},
		{from: "var.a", to: "local.ladder_in_0"},
		{from: "data.aws_x.y[*].arn", to: "local.ladder_in_2"},
	}
	for i := range rs {
		rs[i].toks = refTokens(rs[i].from)
	}
	slices.SortStableFunc(rs, func(x, y rename) int { return len(y.from) - len(x.from) })
	for _, c := range []struct{ in, want string }{
		{"var.a", "local.ladder_in_0"},
		{"var.ab", "local.ladder_in_1"},
		{"[var.a, var.ab, xvar.a, a.var.a, var.ax, var.a-b]", "[local.ladder_in_0, local.ladder_in_1, xvar.a, a.var.a, var.ax, var.a-b]"},
		{"var.a[0].b", "local.ladder_in_0[0].b"},
		{"var . a", "var . a"},
		{`"var.a ${var.a}"`, `"var.a ${local.ladder_in_0}"`},
		{"<<EOT\nvar.a ${var.a}\nEOT\n", "<<EOT\nvar.a ${local.ladder_in_0}\nEOT\n"},
		{"var.a # var.a", "local.ladder_in_0 # var.a"},
		{"concat(data.aws_x.y[*].arn, data.aws_x.y[0].arn)", "concat(local.ladder_in_2, data.aws_x.y[0].arn)"},
		{"var.a +", "local.ladder_in_0 +"}, // a tail that does not parse is renamed all the same
		{"", ""},
	} {
		if got := renameRefs(c.in, rs); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

// hostileTrigger is the first oracle trigger with one key replaced.
func hostileTrigger(t *testing.T, key string, v any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(readTD(t, filepath.Join(buildTD, "ladder-pass-01", "trigger.json")), &m); err != nil {
		t.Fatal(err)
	}
	m[key] = v
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestBuildHostile feeds inputs a hostile tree or report could produce: each
// is refused with its fixed reason or is an error, and none builds a file
// outside the fixture's own paths.
func TestBuildHostile(t *testing.T) {
	tmpl := readTD(t, filepath.Join(buildTD, "templates", "l1.tf.tmpl"))
	deep := strings.Repeat("list(", 21) + "string" + strings.Repeat(")", 21)
	wide := "tuple([" + strings.Repeat("string, ", 200) + "])"
	refused := []struct {
		name, key string
		v         any
		reason    string
	}{
		{"type nested over 20 deep", "referenced_inputs", []any{map[string]any{"name": "vpc_security_group_ids", "type": deep}}, "an input type is outside the L1 type grammar"},
		{"type over 400 tokens", "referenced_inputs", []any{map[string]any{"name": "vpc_security_group_ids", "type": wide}}, "an input type is outside the L1 type grammar"},
		{"type with a quote", "referenced_inputs", []any{map[string]any{"name": "vpc_security_group_ids", "type": `list("string")`}}, "an input type is outside the L1 type grammar"},
		{"type with a comment", "referenced_inputs", []any{map[string]any{"name": "vpc_security_group_ids", "type": "list(#x\nstring)"}}, "an input type is outside the L1 type grammar"},
		{"type with an expression", "referenced_inputs", []any{map[string]any{"name": "vpc_security_group_ids", "type": "list(file(x))"}}, "an input type is outside the L1 type grammar"},
		{"input name with a path", "referenced_inputs", []any{map[string]any{"name": "../x", "type": "string"}}, "a referenced input has no name"},
		{"case id with a path", "cases", []any{map[string]any{"id": "../../etc/x", "inputs": map[string]any{"vpc_security_group_ids": map[string]any{"value": []any{}}}}}, "a case has no id or inputs"},
		{"case id with a slash", "cases", []any{map[string]any{"id": "a/b", "inputs": map[string]any{"vpc_security_group_ids": map[string]any{"value": []any{}}}}}, "a case has no id or inputs"},
		{"case id over 64", "cases", []any{map[string]any{"id": strings.Repeat("a", 65), "inputs": map[string]any{"vpc_security_group_ids": map[string]any{"value": []any{}}}}}, "a case has no id or inputs"},
		{"reference text over 256", "contextual_ref_details", []any{map[string]any{"kind": "resource", "text": "aws_x.y." + strings.Repeat("a", 250)}}, "a contextual reference has no text and kind"},
		{"cases not a list", "cases", map[string]any{}, "no cases"},
	}
	for _, c := range refused {
		b, err := Build(BuildOptions{Level: "L1", Template: tmpl, Trigger: hostileTrigger(t, c.key, c.v)})
		if err != nil || b.OK || b.Result["reason"] != c.reason || len(b.Files) != 0 {
			t.Errorf("%s: %v %v", c.name, err, b)
		}
	}
	errs := []struct {
		name string
		o    BuildOptions
	}{
		{"not JSON", BuildOptions{Level: "L1", Template: tmpl, Trigger: []byte("{")}},
		{"not an object", BuildOptions{Level: "L1", Template: tmpl, Trigger: []byte("[]")}},
		{"two values", BuildOptions{Level: "L1", Template: tmpl, Trigger: []byte("{} {}")}},
		{"invalid UTF-8", BuildOptions{Level: "L1", Template: tmpl, Trigger: []byte("{\"a\":\"\xff\"}")}},
		{"nested over 512", BuildOptions{Level: "L1", Template: tmpl, Trigger: []byte(`{"a":` + strings.Repeat("[", 600) + strings.Repeat("]", 600) + "}")}},
		{"a number out of range", BuildOptions{Level: "L1", Template: tmpl, Trigger: hostileTrigger(t, "cases", []any{map[string]any{"id": "a", "inputs": map[string]any{"vpc_security_group_ids": map[string]any{"value": json.Number("1e999")}}}})}},
		{"a template over its cap", BuildOptions{Level: "L1", Template: bytes.Repeat([]byte("x"), maxTemplateBytes+1), Trigger: hostileTrigger(t, "id", "x")}},
		{"a template without its sections", BuildOptions{Level: "L1", Template: []byte("#-- standin\n"), Trigger: hostileTrigger(t, "id", "x")}},
		{"another level", BuildOptions{Level: "L4", Template: tmpl, Trigger: hostileTrigger(t, "id", "x")}},
	}
	for _, c := range errs {
		if b, err := Build(c.o); err == nil {
			t.Errorf("%s: built %v", c.name, b.Result)
		}
	}
}

// allowedPath is a path the builder may write: a case var file, a side's
// files, or a side's manifest.
var allowedPath = regexp.MustCompile(`\A(?:vars/[A-Za-z0-9_.:-]{1,64}\.tfvars\.json|(?:base|head)/(?:main\.tf|subject\.tf|ladder\.tftest\.hcl)|expect/(?:base|head)\.json)\z`)

// FuzzBuild feeds arbitrary triggers at each level. No input may panic;
// a build writes only the fixture's own paths, each once, and every JSON file
// it writes parses.
func FuzzBuild(f *testing.F) {
	tmpl, err := os.ReadFile(filepath.Join(buildTD, "templates", "l1.tf.tmpl"))
	if err != nil {
		f.Fatal(err)
	}
	l3, err := os.ReadFile(filepath.Join(buildTD, "templates", "aws_rds_cluster.tf.tmpl"))
	if err != nil {
		f.Fatal(err)
	}
	dirs, err := filepath.Glob(filepath.Join(buildTD, "*", "trigger.json"))
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range dirs {
		data, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data, uint8(0))
	}
	f.Fuzz(func(t *testing.T, trigger []byte, lv uint8) {
		o := BuildOptions{Trigger: trigger, Template: tmpl}
		switch lv % 4 {
		case 0, 1:
			o.Level = "L1"
		case 2:
			o.Level, o.Attribute = "L2", "for_each"
		case 3:
			o.Level, o.Attribute, o.Resource, o.ResourceTemplate = "L3", "vpc_security_group_ids", "aws_rds_cluster", l3
		}
		b, err := Build(o)
		if err != nil {
			return
		}
		if _, err := EncodeResult(b.Result); err != nil {
			t.Fatalf("result does not encode: %v", err)
		}
		if !b.OK {
			if len(b.Files) != 0 {
				t.Fatal("a refusal has files")
			}
			r, _ := b.Result["reason"].(string)
			if r == "" || utf8.RuneCountInString(r) > maxReasonChars {
				t.Fatalf("reason %q", r)
			}
			return
		}
		seen := map[string]bool{}
		for _, fl := range b.Files {
			if !allowedPath.MatchString(fl.Path) || seen[fl.Path] {
				t.Fatalf("path %q", fl.Path)
			}
			seen[fl.Path] = true
			if strings.HasSuffix(fl.Path, ".json") && !json.Valid(fl.Data) {
				t.Fatalf("%s is not JSON: %q", fl.Path, fl.Data)
			}
		}
	})
}

// TestBuildWritesUTF8 pins the other place the first builder differs: a
// \u escape of a character below U+0100 in the trigger. That builder wrote
// it as one Latin-1 byte, which is not valid UTF-8, unless the same output
// held a wider character; this one always writes UTF-8. A behaviour report
// writes such characters as UTF-8 bytes, which both builders copy as they
// are.
func TestBuildWritesUTF8(t *testing.T) {
	tmpl := readTD(t, filepath.Join(buildTD, "templates", "l1.tf.tmpl"))
	trig := hostileTrigger(t, "cases", []any{map[string]any{"id": "a", "inputs": map[string]any{"vpc_security_group_ids": map[string]any{"value": []any{"PLACE"}}}}})
	trig = bytes.Replace(trig, []byte("PLACE"), []byte(`\u00e9`), 1) // a JSON escape, not the character
	if !bytes.Contains(trig, []byte(`"\u00e9"`)) {
		t.Fatalf("trigger %s holds no escape", trig)
	}
	b, err := Build(BuildOptions{Level: "L1", Template: tmpl, Trigger: trig})
	if err != nil || !b.OK {
		t.Fatal(err, b.Result)
	}
	seen := false
	for _, f := range b.Files {
		if f.Path == "vars/a.tfvars.json" {
			seen = true
			if !bytes.Contains(f.Data, []byte("\"\xc3\xa9\"")) || !utf8.Valid(f.Data) {
				t.Errorf("%s: %q, want the UTF-8 bytes c3 a9", f.Path, f.Data)
			}
		}
	}
	if !seen {
		t.Error("no var file for case a")
	}
}

// TestFormatFloat holds the float text to what the first builder printed
// for each value, read from Perl's JSON::PP 4.16: digits for a whole value
// written with an exponent and no point, or with both past 2^53; 15
// significant digits for any other.
func TestFormatFloat(t *testing.T) {
	for in, want := range map[string]string{
		"1e14": "100000000000000", "1e15": "1000000000000000", "4.5e16": "45000000000000000",
		"1e17": "100000000000000000", "99999999999999999e2": "10000000000000000000", "1e19": "10000000000000000000",
		"1.8e19": "18000000000000000000", "1e20": "1e+20", "1.5": "1.5", "123456789012345.6": "123456789012346",
		"1e300": "1e+300", "0.0": "0", "-0.0": "0", "1e-5": "1e-05", "1234567890123456e3": "1234567890123456000",
		"-1e15": "-1000000000000000", "-9.2e18": "-9200000000000000000", "-9.223372036854775808e18": "-9223372036854775808",
		"-9.3e18": "-9.3e+18", "1.8446744073709551615e19": "1.84467440737096e+19", "1e16": "10000000000000000",
		"18446744073709551616": "1.84467440737096e+19", "-9999999999999999999": "-1e+19",
		"7.878802e15": "7.878802e+15", "7878802e9": "7878802000000000", "9.223372036854775e15": "9223372036854776",
		"4.5e15": "4.5e+15", "9.0e15": "9e+15", "9.1e15": "9100000000000000", "9.007199254740992e15": "9.00719925474099e+15",
		"9.007199254740994e15": "9007199254740994", "-9.007199254740994e15": "-9007199254740994", "1.8e17": "180000000000000000",
		"180000000000000000.0": "1.8e+17", "9223372036854775807.0": "9.22337203685478e+18", "1.0e15": "1e+15",
		"25E-1": "2.5", "1E2": "100", "1.5E16": "15000000000000000", "2.25e1": "22.5", "2.5e1": "25", "3.000": "3",
	} {
		got, isNum, err := number(json.Number(in))
		if err != nil || !isNum || got != want {
			t.Errorf("%s: %q %v %v, want %q", in, got, isNum, err, want)
		}
	}
	if _, _, err := number("1e309"); err == nil {
		t.Error("1e309 is not refused") // the first builder wrote Inf, which is no JSON
	}
}

// TestBuildRefusesFoldedCaseIDs: two case ids that differ only in letter
// case would share one var file on a case-insensitive file system, so the
// known case would plan the other's inputs. They are refused on every file
// system, with no file; the first builder built them.
func TestBuildRefusesFoldedCaseIDs(t *testing.T) {
	tmpl := readTD(t, filepath.Join(buildTD, "templates", "l1.tf.tmpl"))
	in := map[string]any{"vpc_security_group_ids": map[string]any{"value": []any{}}}
	unk := map[string]any{"vpc_security_group_ids": map[string]any{"value": nil, "unknown": true}}
	trig := hostileTrigger(t, "cases", []any{map[string]any{"id": "Typical", "inputs": in}, map[string]any{"id": "typical", "inputs": unk}})
	b, err := Build(BuildOptions{Level: "L1", Template: tmpl, Trigger: trig})
	if err != nil || b.OK || b.Result["reason"] != "two case ids differ only in letter case" || len(b.Files) != 0 {
		t.Fatalf("%v %v", err, b)
	}
}

// TestWriteFilesCleansUp: a write that fails part way removes every file
// and directory it made, so --out stays empty. A path given twice fails at
// its second exclusive create; without O_EXCL it would overwrite.
func TestWriteFilesCleansUp(t *testing.T) {
	out := t.TempDir()
	files := []File{{Path: "vars/a.tfvars.json", Data: []byte("{}")}, {Path: "base/main.tf", Data: []byte("x")}, {Path: "base/main.tf", Data: []byte("y")}}
	if err := WriteFiles(out, files); err == nil {
		t.Fatal("a path written twice")
	}
	if ents, err := os.ReadDir(out); err != nil || len(ents) != 0 {
		t.Errorf("%d entries left: %v", len(ents), err)
	}
}
