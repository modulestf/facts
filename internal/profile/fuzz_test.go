package profile

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"testing"
)

// FuzzNew feeds an arbitrary PROFILE.md, beside the templates of the test
// profile, to the profile reader. A refusal returns no profile. An accepted
// profile has a SHA-256 and only templates that New was given, each holding
// that template's bytes.
func FuzzNew(f *testing.F) {
	const dir = "../../testdata/profile"
	files := map[string][]byte{}
	entries, err := os.ReadDir(filepath.Join(dir, TemplateDir))
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, TemplateDir, e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		files[path.Join(TemplateDir, e.Name())] = data
	}
	for _, p := range []string{filepath.Join(dir, File), "../../testdata/monorepo-downgrade/profile/PROFILE.md"} {
		doc, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(doc)
	}
	f.Fuzz(func(t *testing.T, doc []byte) {
		in := map[string][]byte{File: doc}
		maps.Copy(in, files)
		p, err := New(in)
		if err != nil {
			if p != nil {
				t.Fatalf("error %v with a profile", err)
			}
			return
		}
		if p == nil || len(p.SHA256) != 64 {
			t.Fatalf("profile %v without a SHA-256", p)
		}
		for _, tm := range p.Templates {
			data, ok := in[path.Join(TemplateDir, tm.Name)]
			if !ok || string(data) != string(tm.Data) {
				t.Fatalf("template %q not as given", tm.Name)
			}
		}
	})
}
