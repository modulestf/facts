package facts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/profile"
)

// profileRule needs the profile and finds nothing with it.
var profileRule = Rule{ID: "profile.zz-needs-profile", Tier: "tool", Class: ClassPath, Scope: ScopeAnyFileInArea,
	Kinds: []string{AreaRoot, AreaSubmodule, AreaExample},
	Check: func(a *Area) Result {
		if res, ok := needProfile(a); !ok {
			return res
		}
		return Result{}
	}}

const profileCase = "../../testdata/versions-missing-positive/head"

// copyProfile copies the test profile to a temp dir a test may change.
func copyProfile(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "profile")
	if err := os.CopyFS(dst, os.DirFS(testProfile)); err != nil {
		t.Fatal(err)
	}
	return dst
}

// profileEntries runs profileRule with the profile in dir and returns its
// manifest entries and the envelope's profile hash.
func profileEntries(t *testing.T, dir string) ([]ManifestEntry, *string) {
	t.Helper()
	r, err := run(Options{Head: profileCase, Limits: DefaultLimits, ProfileDir: dir}, []Rule{profileRule})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Manifest) == 0 {
		t.Fatal("no manifest entry")
	}
	return r.Manifest, r.Envelope.ProfileSHA256
}

// TestProfileSHA256InEnvelope checks a run with the test profile records the
// hash of the documented preimage, built here from the files on disk.
func TestProfileSHA256InEnvelope(t *testing.T) {
	m, sum := profileEntries(t, testProfile)
	files := map[string][]byte{}
	doc, err := os.ReadFile(filepath.Join(testProfile, profile.File))
	if err != nil {
		t.Fatal(err)
	}
	files[profile.File] = doc
	tmpl, err := filepath.Glob(filepath.Join(testProfile, profile.TemplateDir, "*"))
	if err != nil || len(tmpl) != 9 {
		t.Fatalf("want the 9 templates of template-destinations, got %d: %v", len(tmpl), err)
	}
	for _, f := range tmpl {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		files[profile.TemplateDir+"/"+filepath.Base(f)] = data
	}
	raw := sha256.Sum256(profile.Preimage(files))
	want := hex.EncodeToString(raw[:])
	if sum == nil || *sum != want {
		t.Fatalf("profile_sha256 %v, want %s", sum, want)
	}
	for _, e := range m {
		if e.Status != StatusClean {
			t.Errorf("%s in %s: %s %s, want clean with the profile", e.RuleID, e.Area, e.Status, e.Reason)
		}
	}
	// Control: a changed template changes the recorded hash.
	dir := copyProfile(t)
	f := filepath.Join(dir, "templates", ".editorconfig.template")
	if err := os.WriteFile(f, append(files["templates/.editorconfig.template"], '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, other := profileEntries(t, dir); other == nil || *other == want {
		t.Errorf("a changed template kept profile_sha256 %v", other)
	}
}

// TestProfileUnavailable checks each way a profile directory can be unusable
// leaves the hash null and makes a rule that needs the profile
// not_evaluated(profile_unavailable), with the reason named.
func TestProfileUnavailable(t *testing.T) {
	for name, c := range map[string]struct {
		mut  func(t *testing.T, dir string)
		want string
	}{
		"no profile input": {nil, noProfile},
		"missing key": {func(t *testing.T, dir string) {
			editProfile(t, dir, "secondary: us-east-1\n", "")
		}, "block regions: missing key secondary"},
		"PROFILE.md is a link": {func(t *testing.T, dir string) {
			moveAndLink(t, filepath.Join(dir, profile.File))
		}, "PROFILE.md is symlink"},
		"template is a link": {func(t *testing.T, dir string) {
			moveAndLink(t, filepath.Join(dir, "templates", ".gitignore.template"))
		}, "templates/.gitignore.template is symlink"},
		"templates is a link": {func(t *testing.T, dir string) {
			moveAndLink(t, filepath.Join(dir, "templates"))
		}, "templates is symlink"},
		"PROFILE.md over the cap": {func(t *testing.T, dir string) {
			editProfile(t, dir, "\n", "\n"+strings.Repeat("x", int(profileLimits.MaxFileBytes))+"\n")
		}, "PROFILE.md is oversize"},
		"template over the cap": {func(t *testing.T, dir string) {
			f := filepath.Join(dir, "templates", ".editorconfig.template")
			if err := os.WriteFile(f, bytes.Repeat([]byte("x"), int(profileLimits.MaxFileBytes)+1), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "templates/.editorconfig.template is oversize"},
		"too many files": {func(t *testing.T, dir string) {
			for i := range profileLimits.MaxFiles {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}, "more than 1000 entries"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := ""
			if c.mut != nil {
				dir = copyProfile(t)
				c.mut(t, dir)
			}
			m, sum := profileEntries(t, dir)
			if sum != nil {
				t.Errorf("profile_sha256 %s, want null", *sum)
			}
			for _, e := range m {
				if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonProfileUnavailable || !strings.Contains(e.Reason, c.want) {
					t.Errorf("%s in %s: %s %s %q, want not_evaluated profile_unavailable with %q", e.RuleID, e.Area, e.Status, e.ReasonCode, e.Reason, c.want)
				}
			}
		})
	}
}

// TestProfileDirMustBeADirectory checks a named profile path that is not a
// directory fails the run instead of being read as no profile.
func TestProfileDirMustBeADirectory(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "file")
	link := filepath.Join(tmp, "link")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(testProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(abs, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(tmp, "missing"), file, link} {
		_, err := Run(Options{Head: profileCase, Limits: DefaultLimits, ProfileDir: dir})
		if err == nil || !strings.Contains(err.Error(), "is not a directory") {
			t.Errorf("%s: error %v, want not a directory", filepath.Base(dir), err)
		}
	}
	// Control: the profile itself is accepted.
	if _, err := Run(Options{Head: profileCase, Limits: DefaultLimits, ProfileDir: testProfile}); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func editProfile(t *testing.T, dir, old, repl string) {
	t.Helper()
	f := filepath.Join(dir, profile.File)
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("PROFILE.md has no %q", old)
	}
	//nolint:gosec // G703: f is in the test's own temp copy of the profile
	if err := os.WriteFile(f, bytes.Replace(data, []byte(old), []byte(repl), 1), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moveAndLink moves p aside and puts a link to it in its place.
func moveAndLink(t *testing.T, p string) {
	t.Helper()
	aside := p + ".real"
	if err := os.Rename(p, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(aside), p); err != nil {
		t.Fatal(err)
	}
}

// TestProfileRulesNeedTheProfile runs every profile.* tool and candidate rule
// without a profile: each entry that applies must be
// not_evaluated(profile_unavailable). No such rule exists yet; profileRule
// stands in as a control, and the loop guards every rule that lands.
func TestProfileRulesNeedTheProfile(t *testing.T) {
	var rules []Rule
	for _, r := range append(slices.Clone(Rules), profileRule) {
		if strings.HasPrefix(r.ID, "profile.") && (r.Tier == "tool" || r.Tier == "candidate") {
			rules = append(rules, r)
		}
	}
	const dir = "../../testdata/variable-untyped-base"
	r, err := run(Options{Head: dir + "/head", Base: dir + "/base", Limits: DefaultLimits}, rules)
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]bool{}
	for _, e := range r.Manifest {
		if e.Status == StatusNotApplicable {
			continue
		}
		checked[e.RuleID] = true
		if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonProfileUnavailable {
			t.Errorf("%s in %s: %s %s without a profile, want not_evaluated profile_unavailable", e.RuleID, e.Area, e.Status, e.ReasonCode)
		}
	}
	if !checked[profileRule.ID] {
		t.Fatal("control rule was not checked")
	}
}

// TestProfileReasonHasNoHostPath checks a profile directory that cannot be
// opened gives a fixed reason, not the host path.
func TestProfileReasonHasNoHostPath(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode 000 directory")
	}
	dir := copyProfile(t)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) //nolint:gosec // G302: restores the temp dir for cleanup
	m, sum := profileEntries(t, dir)
	for _, e := range m {
		if e.ReasonCode != ReasonProfileUnavailable || e.Reason != "profile: profile directory unreadable" || sum != nil {
			t.Errorf("%s in %s: %s %q, hash %v", e.RuleID, e.Area, e.ReasonCode, e.Reason, sum)
		}
	}
}
