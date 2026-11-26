package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const preCommitFixtures = "../../testdata/pre-commit"

// fixtureDirs lists the case directories of one testdata/pre-commit set.
func fixtureDirs(t *testing.T, set string) []string {
	t.Helper()
	ds, err := filepath.Glob(filepath.Join(preCommitFixtures, set, "*"))
	if err != nil || len(ds) == 0 {
		t.Fatalf("no %s fixtures: %v", set, err)
	}
	return ds
}

// optional is the path of name in dir, which the command reads as absent
// when the fixture has no such file.
func optional(dir, name string) string { return filepath.Join(dir, name) }

func readWant(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestPreCommitDeriveFixtures runs derive on each case: the bytes of want.tf
// and exit 0, or no output and exit 3 when the case has no want.tf.
func TestPreCommitDeriveFixtures(t *testing.T) {
	for _, d := range fixtureDirs(t, "derive") {
		code, out, errOut := runCLI([]string{"pre-commit", "derive", filepath.Join(d, "in.tf")})
		want, err := os.ReadFile(filepath.Join(d, "want.tf"))
		switch {
		case os.IsNotExist(err):
			if code != exitUndefined || out != "" || errOut != "" {
				t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 3 and nothing", filepath.Base(d), code, out, errOut)
			}
		case err != nil:
			t.Fatal(err)
		case code != exitClean || out != string(want) || errOut != "":
			t.Errorf("%s: exit %d, stderr %q, stdout differs: %v", filepath.Base(d), code, errOut, out != string(want))
		}
	}
}

// TestPreCommitDeriveRecorded checks the recorded wrappers are derive of
// their modules, and their own derive.
func TestPreCommitDeriveRecorded(t *testing.T) {
	for _, r := range []string{"terraform-aws-cloudfront", "terraform-aws-s3-bucket", "terraform-aws-vpc"} {
		dir := filepath.Join(preCommitFixtures, "recorded", r)
		want := readWant(t, filepath.Join(dir, "wrappers", "versions.tf"))
		for _, in := range []string{"versions.tf", "wrappers/versions.tf"} {
			if code, out, _ := runCLI([]string{"pre-commit", "derive", filepath.Join(dir, in)}); code != exitClean || out != want {
				t.Errorf("%s %s: exit %d, derive is not the recorded wrapper", r, in, code)
			}
		}
	}
}

// TestPreCommitDeriveSize checks the 64 KiB bound: exactly 64 KiB is
// defined, one byte more is not.
func TestPreCommitDeriveSize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.tf")
	body := strings.Repeat("#", 65535) + "\n"
	for _, c := range []struct {
		data string
		code int
	}{{body, exitClean}, {body + "\n", exitUndefined}} {
		if err := os.WriteFile(p, []byte(c.data), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out, _ := runCLI([]string{"pre-commit", "derive", p}); code != c.code || (code == exitClean) != (out == c.data) {
			t.Errorf("%d bytes: exit %d, stdout %d bytes", len(c.data), code, len(out))
		}
	}
}

// TestPreCommitWrapperFixtures runs wrapper on each case; config.yaml may be
// absent.
func TestPreCommitWrapperFixtures(t *testing.T) {
	for _, d := range fixtureDirs(t, "wrapper") {
		code, out, errOut := runCLI([]string{"pre-commit", "wrapper", "--config", optional(d, "config.yaml"),
			"--module", filepath.Join(d, "module.tf"), "--wrapper", filepath.Join(d, "wrapper.tf")})
		if want := readWant(t, filepath.Join(d, "want")); code != exitClean || out != want || errOut != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %q", filepath.Base(d), code, out, errOut, want)
		}
	}
}

// TestPreCommitDocsFixtures runs docs on each case with the profile version;
// config.yaml and workflow.yml may be absent.
func TestPreCommitDocsFixtures(t *testing.T) {
	for _, d := range fixtureDirs(t, "docs") {
		code, out, errOut := runCLI([]string{"pre-commit", "docs", "--config", optional(d, "config.yaml"),
			"--workflow", optional(d, "workflow.yml"), "--version", "v0.24.0"})
		if want := readWant(t, filepath.Join(d, "want")); code != exitClean || out != want || errOut != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %q", filepath.Base(d), code, out, errOut, want)
		}
	}
}

// TestPreCommitFileStates checks the files a host could hand over that are
// not regular: each is a reason, never a read through a link.
func TestPreCommitFileStates(t *testing.T) {
	tmp := t.TempDir()
	vpc := filepath.Join(preCommitFixtures, "wrapper", "derived")
	w := func(name, data string) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg, mod, wrp := filepath.Join(vpc, "config.yaml"), filepath.Join(vpc, "module.tf"), filepath.Join(vpc, "wrapper.tf")
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(abs(wrp), link); err != nil {
		t.Fatal(err)
	}
	big := w("big", strings.Repeat("#", 65537))
	nope := filepath.Join(tmp, "nope")
	cases := []struct {
		name, config, module, wrapper, want string
	}{
		{"wrapper absent", cfg, mod, nope, "unknown wrapper absent"},
		{"wrapper link", cfg, mod, link, "unknown wrapper link"},
		{"wrapper directory", cfg, mod, tmp, "unknown wrapper not a regular file"},
		{"wrapper over size", cfg, mod, big, "unknown wrapper over size"},
		{"module absent", cfg, nope, wrp, "unknown module absent"},
		{"module link", cfg, link, wrp, "unknown module link"},
		{"module over size", cfg, big, wrp, "unknown module over size"},
		{"wrapper before module", cfg, nope, nope, "unknown wrapper absent"},
		{"file before pin", nope, nope, wrp, "unknown module absent"},
		{"config link", link, mod, wrp, "unknown pin unreadable"},
		{"config over size", big, mod, wrp, "unknown pin unreadable"},
		{"config directory", tmp, mod, wrp, "unknown pin unreadable"},
		{"config absent", nope, mod, wrp, "unknown no pin"},
	}
	for _, c := range cases {
		code, out, _ := runCLI([]string{"pre-commit", "wrapper", "--config", c.config, "--module", c.module, "--wrapper", c.wrapper})
		if code != exitClean || out != c.want+"\n" {
			t.Errorf("%s: exit %d, stdout %q", c.name, code, out)
		}
	}
	// docs: a link or an oversized file is custom and unreadable; derive of a
	// link is undefined.
	tpl := filepath.Join(preCommitFixtures, "docs", "template", "config.yaml")
	cl := filepath.Join(tmp, "cl")
	if err := os.Symlink(abs(tpl), cl); err != nil {
		t.Fatal(err)
	}
	huge := w("huge", "terraform-docs-version: v0.24.0\n"+strings.Repeat("#", 1<<20))
	if code, out, _ := runCLI([]string{"pre-commit", "docs", "--config", cl, "--workflow", huge, "--version", "v0.24.0"}); code != exitClean || out != "hook custom\nworkflow unreadable\n" {
		t.Errorf("docs link and oversize: exit %d, stdout %q", code, out)
	}
	if code, out, _ := runCLI([]string{"pre-commit", "docs", "--config", tpl}); code != exitClean || out != "hook profile\n" {
		t.Errorf("docs without a workflow: exit %d, stdout %q", code, out)
	}
	if code, out, _ := runCLI([]string{"pre-commit", "derive", link}); code != exitUndefined || out != "" {
		t.Errorf("derive of a link: exit %d, stdout %q", code, out)
	}
}

// TestPreCommitUsage checks the usage errors.
func TestPreCommitUsage(t *testing.T) {
	tpl := filepath.Join(preCommitFixtures, "docs", "template", "config.yaml")
	for _, args := range [][]string{
		{"pre-commit"},
		{"pre-commit", "derive"},
		{"pre-commit", "wrapper", "--config", tpl, "--module", tpl},
		{"pre-commit", "docs"},
		{"pre-commit", "docs", "--config", tpl, "--workflow", tpl},
		{"pre-commit", "docs", "--config", tpl, "--version", "v0.24.0"},
		{"pre-commit", "docs", "--config", tpl, "--workflow", tpl, "--version", "0.24.0"},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || errOut == "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
