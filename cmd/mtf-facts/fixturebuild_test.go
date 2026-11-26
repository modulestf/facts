package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureBuildTD = "../../testdata/fixture-build"

// fixtureBuildArgs are the fixture-build arguments of an oracle case, with
// a new empty --out.
func fixtureBuildArgs(t *testing.T, name string) []string {
	t.Helper()
	dir := filepath.Join(fixtureBuildTD, name)
	data, err := os.ReadFile(filepath.Join(dir, "args.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Level, Attribute, Resource, Template string
		ResourceTemplate                     string `json:"resource_template"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	tmpl := func(n string) string {
		if strings.HasPrefix(n, "./") {
			return filepath.Join(dir, n)
		}
		return filepath.Join(fixtureBuildTD, "templates", n)
	}
	args := []string{"fixture-build", filepath.Join(dir, "trigger.json"), "--template", tmpl(a.Template),
		"--out", t.TempDir(), "--level", a.Level}
	if a.Attribute != "" {
		args = append(args, "--attribute", a.Attribute)
	}
	if a.Resource != "" {
		args = append(args, "--resource", a.Resource, "--resource-template", tmpl(a.ResourceTemplate))
	}
	return args
}

// TestFixtureBuildCLI runs each oracle case through the command: the exit
// code, stdout byte for byte and, when built, the files under --out.
func TestFixtureBuildCLI(t *testing.T) {
	ents, err := os.ReadDir(fixtureBuildTD)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() || e.Name() == "templates" {
			continue
		}
		dir := filepath.Join(fixtureBuildTD, e.Name())
		var want struct{ Exit int }
		data, err := os.ReadFile(filepath.Join(dir, "want.json"))
		if err != nil || json.Unmarshal(data, &want) != nil {
			t.Fatalf("%s: want.json: %v", e.Name(), err)
		}
		args := fixtureBuildArgs(t, e.Name())
		out := args[5]
		code, stdout, stderr := runCLI(args)
		if code != want.Exit {
			t.Errorf("%s: exit %d, want %d, stderr %q", e.Name(), code, want.Exit, stderr)
			continue
		}
		files, err := os.ReadDir(out)
		if err != nil {
			t.Fatal(err)
		}
		if want.Exit != 0 {
			if len(files) != 0 {
				t.Errorf("%s: exit %d with files written", e.Name(), code)
			}
			if want.Exit == 2 && (stdout != "" || !strings.HasPrefix(stderr, "mtf-facts: fixture-build: ")) {
				t.Errorf("%s: stdout %q, stderr %q", e.Name(), stdout, stderr)
			}
			if want.Exit == 2 {
				continue
			}
		}
		if wantOut, _ := os.ReadFile(filepath.Join(dir, "stdout.json")); stdout != string(wantOut) || stderr != "" {
			t.Errorf("%s: stdout %q, stderr %q", e.Name(), stdout, stderr)
		}
		if want.Exit == 0 {
			sub, _ := os.ReadDir(filepath.Join(dir, "tree"))
			if len(files) != len(sub) {
				t.Errorf("%s: %d entries written, want %d", e.Name(), len(files), len(sub))
			}
		}
	}
}

// TestFixtureBuildRefusesInput checks the usage errors and the input files
// the command refuses: each exits 2 with nothing on stdout and no file.
func TestFixtureBuildRefusesInput(t *testing.T) {
	base := fixtureBuildArgs(t, "ladder-pass-01")
	trig, tmpl := base[1], base[3]
	tmp := t.TempDir()
	link := filepath.Join(tmp, "link.json")
	if err := os.Symlink(trig, link); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(tmp, "big.json")
	if err := os.WriteFile(big, []byte(`{"a":"`+strings.Repeat("x", 4<<20)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	outLink := filepath.Join(tmp, "out")
	if err := os.Symlink(t.TempDir(), outLink); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		err  string
	}{
		{"no template", []string{"fixture-build", trig, "--out", t.TempDir()}, "--template is required"},
		{"no out", []string{"fixture-build", trig, "--template", tmpl}, "--out is required"},
		{"L1 with an attribute", []string{"fixture-build", trig, "--template", tmpl, "--out", t.TempDir(), "--attribute", "count"}, "not taken at L1"},
		{"L2 without an attribute", []string{"fixture-build", trig, "--template", tmpl, "--out", t.TempDir(), "--level", "L2"}, "--attribute is required at L2"},
		{"L2 with a resource", []string{"fixture-build", trig, "--template", tmpl, "--out", t.TempDir(), "--level", "L2", "--attribute", "count", "--resource", "x"}, "taken at L3 only"},
		{"L3 without a resource", []string{"fixture-build", trig, "--template", tmpl, "--out", t.TempDir(), "--level", "L3", "--attribute", "x"}, "required at L3"},
		{"another level", []string{"fixture-build", trig, "--template", tmpl, "--out", t.TempDir(), "--level", "L4"}, "--level must be"},
		{"a linked trigger", []string{"fixture-build", link, "--template", tmpl, "--out", t.TempDir()}, "trigger: not a regular file"},
		{"a directory trigger", []string{"fixture-build", tmp, "--template", tmpl, "--out", t.TempDir()}, "trigger: not a regular file"},
		{"a trigger over 4 MiB", []string{"fixture-build", big, "--template", tmpl, "--out", t.TempDir()}, "trigger: over 4194304 bytes"},
		{"a missing template", []string{"fixture-build", trig, "--template", filepath.Join(tmp, "none"), "--out", t.TempDir()}, "template: "},
		{"an out that is not empty", []string{"fixture-build", trig, "--template", tmpl, "--out", full}, "is not empty"},
		{"an out that is a link", []string{"fixture-build", trig, "--template", tmpl, "--out", outLink}, "is not a directory"},
		{"an out that is missing", []string{"fixture-build", trig, "--template", tmpl, "--out", filepath.Join(tmp, "none")}, "no such file"},
	} {
		code, out, errOut := runCLI(c.args)
		if code != exitError || out != "" || !strings.Contains(errOut, c.err) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
	}
	if ents, _ := os.ReadDir(full); len(ents) != 1 {
		t.Errorf("the full --out holds %d entries", len(ents))
	}
}
