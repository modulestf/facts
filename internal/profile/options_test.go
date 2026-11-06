package profile

import (
	"slices"
	"strings"
	"testing"
)

// withDoc returns the test profile's files with PROFILE.md edited by edit.
func withDoc(t *testing.T, edit func(string) string) map[string][]byte {
	t.Helper()
	files := testFiles(t)
	files[File] = []byte(edit(string(files[File])))
	return files
}

// optionsDoc is a profile-options block with the given lines after the
// name line.
func optionsDoc(lines ...string) string {
	return "\n```profile-constants\nname: profile-options\n" + strings.Join(lines, "\n") + "\n```\n"
}

// emptyExamples writes the two example entries of required-files empty.
func emptyExamples(doc string) string {
	doc = strings.Replace(doc, "example: main.tf outputs.tf versions.tf README.md\n", "example:\n", 1)
	return strings.Replace(doc, "example-dirs: examples/basic examples/complete\n", "example-dirs:\n", 1)
}

func TestNewOptionsDefaultWithoutBlock(t *testing.T) {
	p, err := New(testFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Options != DefaultOptions {
		t.Errorf("options %+v, want the defaults %+v", p.Options, DefaultOptions)
	}
	if p.Options.Relaxing() {
		t.Error("the default options are relaxing")
	}
}

func TestNewOptionsBlock(t *testing.T) {
	files := withDoc(t, func(doc string) string {
		return emptyExamples(doc) + optionsDoc("format: 1", "consumers: in-repo", "partitions: fixed", "examples: none", "release: none")
	})
	p, err := New(files)
	if err != nil {
		t.Fatal(err)
	}
	want := Options{Format: 1, Consumers: ConsumersInRepo, Partitions: "fixed", Examples: "none", Release: "none"}
	if p.Options != want {
		t.Errorf("options %+v, want %+v", p.Options, want)
	}
	if !p.Options.Relaxing() {
		t.Error("in-repo options are not relaxing")
	}
	if p.RequiredFiles.Example != nil || p.RequiredFiles.ExampleDirs != nil {
		t.Errorf("empty example entries read as %q and %q", p.RequiredFiles.Example, p.RequiredFiles.ExampleDirs)
	}
}

// TestNewOptionsRelaxingByOneValue checks each non-default value alone is
// relaxing.
func TestNewOptionsRelaxingByOneValue(t *testing.T) {
	for _, line := range []string{"consumers: in-repo", "partitions: fixed", "release: none"} {
		t.Run(line, func(t *testing.T) {
			lines := []string{"format: 1", "consumers: public", "partitions: portable", "examples: required", "release: registry"}
			key, _, _ := strings.Cut(line, ":")
			for i, l := range lines {
				if strings.HasPrefix(l, key+":") {
					lines[i] = line
				}
			}
			p, err := New(withDoc(t, func(doc string) string { return doc + optionsDoc(lines...) }))
			if err != nil {
				t.Fatal(err)
			}
			if !p.Options.Relaxing() {
				t.Errorf("%s is not relaxing", line)
			}
		})
	}
}

func TestNewOptionsRefused(t *testing.T) {
	all := []string{"format: 1", "consumers: public", "partitions: portable", "examples: required", "release: registry"}
	replace := func(old, repl string) []string {
		out := slices.Clone(all)
		for i, l := range out {
			if l == old {
				out[i] = repl
			}
		}
		return slices.DeleteFunc(out, func(s string) bool { return s == "" })
	}
	for name, c := range map[string]struct {
		edit func(string) string
		want string
	}{
		"unknown format": {func(d string) string { return d + optionsDoc(replace("format: 1", "format: 2")...) }, "format"},
		"unknown value":  {func(d string) string { return d + optionsDoc(replace("consumers: public", "consumers: everyone")...) }, "consumers"},
		"unknown key":    {func(d string) string { return d + optionsDoc(append(slices.Clone(all), "audience: me")...) }, "unknown key"},
		"missing key":    {func(d string) string { return d + optionsDoc(replace("release: registry", "")...) }, "missing key"},
		"two values": {func(d string) string {
			return d + optionsDoc(replace("release: registry", "release: none registry")...)
		}, "release"},
		"unknown block":     {func(d string) string { return d + "\n```profile-constants\nname: extras\nkey: v\n```\n" }, "unknown block"},
		"none with example": {func(d string) string { return d + optionsDoc(replace("examples: required", "examples: none")...) }, "examples: none"},
		"none with example dirs": {func(d string) string {
			d = strings.Replace(d, "example: main.tf outputs.tf versions.tf README.md\n", "example:\n", 1)
			return d + optionsDoc(replace("examples: required", "examples: none")...)
		}, "examples: none"},
		"required with empty example": {func(d string) string { return emptyExamples(d) }, "empty"},
		"empty value elsewhere":       {func(d string) string { return strings.Replace(d, "primary: eu-west-1\n", "primary:\n", 1) }, "empty"},
		"empty format":                {func(d string) string { return d + optionsDoc(replace("format: 1", "format:")...) }, "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(withDoc(t, c.edit))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %v, want one naming %q", err, c.want)
			}
		})
	}
}

func TestGatedRules(t *testing.T) {
	relaxed := Options{Format: 1, Consumers: ConsumersInRepo, Partitions: "fixed", Examples: "none", Release: "none"}
	for _, id := range []string{
		"structure.arn-partition-hardcoded",
		"examples.disabled-call-missing", "examples.submodule-missing", "examples.versions-missing",
		"examples.feature-undemonstrated", "examples.argument-undemonstrated",
		"profile.commit-type-mismatch", "profile.title-uninformative", "profile.title-misspelling",
		"profile.unrelated-change-bundled", "profile.changelog-handwritten", "profile.release-config-changed",
		"profile.owner-identifier", "profile.provider-meta-missing", "profile.migration-notes-misplaced", "profile.template-drift",
	} {
		if !relaxed.Gated(id) {
			t.Errorf("%s is not gated by %+v", id, relaxed)
		}
		if DefaultOptions.Gated(id) {
			t.Errorf("%s is gated by the default options", id)
		}
	}
	for _, id := range []string{"profile.required-file-missing", "compat.variable-removed", "examples.stale-reference", "structure.missing-description"} {
		if relaxed.Gated(id) {
			t.Errorf("%s is gated, but no option lists it", id)
		}
	}
	// One option gates only its own rules.
	only := DefaultOptions
	only.Partitions = "fixed"
	if !only.Gated("structure.arn-partition-hardcoded") || only.Gated("examples.versions-missing") || only.Gated("profile.template-drift") {
		t.Error("partitions: fixed gates rules of another option")
	}
}

func TestGateTableWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range Gates {
		if _, ok := optionValues[g.Option]; !ok || !slices.Contains(optionValues[g.Option], g.Value) || g.Value == defaultValue(g.Option) {
			t.Errorf("gate %s: %s is not a relaxing value of a known option", g.Option, g.Value)
		}
		for _, id := range g.Rules {
			if seen[id] {
				t.Errorf("%s is gated twice", id)
			}
			seen[id] = true
		}
	}
	if !slices.IsSorted(DowngradeRules) || len(DowngradeRules) != 5 {
		t.Errorf("downgrade rules %q are not the five sorted ids", DowngradeRules)
	}
}
