package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
)

const testDir = "../../testdata/profile"

// testFiles reads every regular file of the test profile, keyed as New wants.
func testFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := fs.WalkDir(os.DirFS(testDir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		data, err := fs.ReadFile(os.DirFS(testDir), p)
		files[p] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestNewReadsEveryBlock(t *testing.T) {
	p, err := New(testFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ got, want []string }{
		"required-files root":              {p.RequiredFiles.Root, []string{"main.tf", "variables.tf", "outputs.tf", "versions.tf", "README.md", "LICENSE"}},
		"required-files submodule":         {p.RequiredFiles.Submodule, []string{"main.tf", "variables.tf", "outputs.tf", "versions.tf", "README.md"}},
		"required-files example":           {p.RequiredFiles.Example, []string{"main.tf", "outputs.tf", "versions.tf", "README.md"}},
		"required-files example-dirs":      {p.RequiredFiles.ExampleDirs, []string{"examples/basic", "examples/complete"}},
		"regions":                          {[]string{p.Regions.Primary, p.Regions.Secondary}, []string{"eu-west-1", "us-east-1"}},
		"argument-order first":             {p.ArgumentOrder.First, []string{"count", "for_each"}},
		"argument-order last":              {p.ArgumentOrder.Last, []string{"tags", "labels"}},
		"profile-owned-files workflow-dir": {[]string{p.ProfileOwned.WorkflowDir}, []string{".github/workflows"}},
		"tooling files":                    {p.Tooling.Files, []string{".checkov.yaml", ".checkov.yml", "trivy.yaml", ".trivyignore", "kics.config", "infracost.yml"}},
		"tooling dirs":                     {p.Tooling.Dirs, []string{".tfsec"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: %q, want %q", name, c.got, c.want)
		}
	}
	if len(p.ProfileOwned.Verbatim) != 8 || p.ProfileOwned.Verbatim[0] != ".editorconfig" {
		t.Errorf("profile-owned-files verbatim: %q", p.ProfileOwned.Verbatim)
	}
	if !strings.HasPrefix(p.Banner, "[![SWUbanner](") || strings.HasSuffix(p.Banner, " ") {
		t.Errorf("banner line: %q", p.Banner)
	}
	if len(p.AdjunctTypes) != 11 || p.AdjunctTypes[0] != "aws_iam_role" {
		t.Errorf("adjunct-types: %q", p.AdjunctTypes)
	}
	if len(p.Templates) != 9 {
		t.Fatalf("template-destinations: %d templates, want 9", len(p.Templates))
	}
	first := p.Templates[0]
	if first.Name != ".editorconfig.template" || first.Destination != ".editorconfig" ||
		!strings.HasPrefix(string(first.Data), "root = true") {
		t.Errorf("first template: %q -> %q, %d bytes", first.Name, first.Destination, len(first.Data))
	}
}

// TestProfileSHA256 rebuilds the documented preimage by hand and checks the
// hash covers PROFILE.md and the named templates, and nothing else.
func TestProfileSHA256(t *testing.T) {
	files := testFiles(t)
	p, err := New(files)
	if err != nil {
		t.Fatal(err)
	}
	used := []string{File}
	for _, tp := range p.Templates {
		used = append(used, TemplateDir+"/"+tp.Name)
	}
	slices.Sort(used)
	var pre strings.Builder
	for _, u := range used {
		sum := sha256.Sum256(files[u])
		pre.WriteString(hex.EncodeToString(sum[:]) + "  " + u + "\n")
	}
	sum := sha256.Sum256([]byte(pre.String()))
	if want := hex.EncodeToString(sum[:]); p.SHA256 != want {
		t.Fatalf("SHA256 %s, want %s from the preimage\n%s", p.SHA256, want, pre.String())
	}

	rehash := func(mut func(map[string][]byte)) string {
		f := maps.Clone(files)
		mut(f)
		q, err := New(f)
		if err != nil {
			t.Fatal(err)
		}
		return q.SHA256
	}
	if rehash(func(f map[string][]byte) { f["notes.md"] = []byte("x") }) != p.SHA256 {
		t.Error("a file the profile does not use changed the hash")
	}
	for _, name := range []string{File, "templates/.editorconfig.template", "templates/.pre-commit-config.yaml.template"} {
		if rehash(func(f map[string][]byte) { f[name] = append(slices.Clone(f[name]), '\n') }) == p.SHA256 {
			t.Errorf("a change to %s did not change the hash", name)
		}
	}
}

// TestNewRefuses mutates PROFILE.md or the file set one way at a time; each
// must fail with an error that names the problem.
func TestNewRefuses(t *testing.T) {
	files := testFiles(t)
	doc := string(files[File])
	replace := func(old, repl string) func(map[string][]byte) {
		return func(f map[string][]byte) {
			if !strings.Contains(doc, old) {
				t.Fatalf("test profile has no %q", old)
			}
			f[File] = []byte(strings.Replace(doc, old, repl, 1))
		}
	}
	cases := map[string]struct {
		mut  func(map[string][]byte)
		want string
	}{
		"no PROFILE.md":      {func(f map[string][]byte) { delete(f, File) }, "no PROFILE.md"},
		"missing template":   {func(f map[string][]byte) { delete(f, "templates/.gitignore.template") }, "no templates/.gitignore.template"},
		"unknown block":      {replace("name: banner\n", "name: banners\n"), "unknown block banners"},
		"duplicate block":    {replace("name: banner\n", "name: regions\n"), `block name "regions" used twice`},
		"unknown key":        {replace("dirs: .tfsec\n", "dirs: .tfsec\nextra: x\n"), "block tooling: unknown key extra"},
		"duplicate key":      {replace("dirs: .tfsec\n", "dirs: .tfsec\ndirs: .x\n"), "key dirs used twice"},
		"empty value":        {replace("dirs: .tfsec\n", "dirs: \n"), "empty value or a stray space"},
		"double space":       {replace("first: count for_each", "first: count  for_each"), "stray space"},
		"trailing space":     {replace("last: tags labels", "last: tags labels "), "stray space"},
		"tab in value":       {replace("first: count for_each", "first: count\tfor_each"), "stray space"},
		"backslash in path":  {replace("dirs: .tfsec", "dirs: a\\b"), "unsafe path"},
		"NUL in destination": {replace(".gitignore.template: .gitignore", ".gitignore.template: a\x00b"), "unsafe template or destination"},
		"name-only template-destinations": {func(f map[string][]byte) {
			start := strings.Index(doc, "name: template-destinations\n") + len("name: template-destinations\n")
			end := start + strings.Index(doc[start:], "```")
			f[File] = []byte(doc[:start] + doc[end:])
		}, "block template-destinations: no template"},
		"carriage return": {replace("last: tags labels", "last: tags labels\r"), "stray space"},
		"no name line":    {replace("name: banner\n", "line0: x\n"), "the first line is not the name line"},
		"nameless block":  {replace("```profile-constants\nname: banner\n", "```profile-constants\n```\n```profile-constants\nname: banner\n"), "a block with no name line"},
		"not key value":   {replace("dirs: .tfsec\n", "dirs .tfsec\n"), "not <key>: <value>"},
		"nested block":    {replace("name: banner\n", "name: banner\n```profile-constants\n"), "a block opens inside another"},
		"unclosed block": {func(f map[string][]byte) {
			f[File] = []byte(strings.TrimRight(strings.TrimSuffix(strings.TrimRight(doc, "\n"), "```"), "\n"))
		}, "not closed"},
		"two regions":         {replace("primary: eu-west-1", "primary: eu-west-1 eu-west-2"), "key primary holds more than one value"},
		"two workflow dirs":   {replace("workflow-dir: .github/workflows", "workflow-dir: a b"), "key workflow-dir holds more than one value"},
		"dotdot path":         {replace("dirs: .tfsec", "dirs: ../.tfsec"), `unsafe path "../.tfsec"`},
		"absolute path":       {replace("example-dirs: examples/basic", "example-dirs: /examples/basic"), "unsafe path"},
		"template with slash": {replace(".editorconfig.template: .editorconfig", "a/.editorconfig.template: .editorconfig"), "not <key>: <value>"},
		"dotdot template":     {replace(".editorconfig.template: .editorconfig", "..: .editorconfig"), "unsafe template or destination"},
		"unsafe destination":  {replace(".gitignore.template: .gitignore", ".gitignore.template: ../.gitignore"), "unsafe template or destination"},
	}
	// A missing key, and a missing block, for every block.
	for name, keys := range blockKeys {
		cases["missing block "+name] = struct {
			mut  func(map[string][]byte)
			want string
		}{replace("name: "+name+"\n", "name: zz-"+name+"\n"), "unknown block zz-" + name}
		for _, k := range keys {
			start := strings.Index(doc, "name: "+name+"\n")
			line := doc[start:]
			i := strings.Index(line, "\n"+k+": ")
			if i < 0 {
				t.Fatalf("block %s has no key %s", name, k)
			}
			line = line[i+1 : i+1+strings.Index(line[i+1:], "\n")+1]
			cases["missing key "+name+" "+k] = struct {
				mut  func(map[string][]byte)
				want string
			}{func(f map[string][]byte) {
				f[File] = []byte(doc[:start] + strings.Replace(doc[start:], line, "", 1))
			}, "block " + name + ": missing key " + k}
		}
	}
	// A block that is gone altogether, not renamed.
	cases["removed block"] = struct {
		mut  func(map[string][]byte)
		want string
	}{replace("```profile-constants\nname: banner\n", "```text\nname: banner\n"), "no block banner"}
	for name, c := range cases {
		f := maps.Clone(files)
		c.mut(f)
		_, err := New(f)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one containing %q", name, err, c.want)
		}
	}
	// Control: the unmutated files build.
	if _, err := New(files); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestPreimageLayout(t *testing.T) {
	got := string(Preimage(map[string][]byte{"b": []byte("x"), "a": nil}))
	want := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  a\n" +
		"2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881  b\n"
	if got != want {
		t.Fatalf("preimage %q, want %q", got, want)
	}
}
