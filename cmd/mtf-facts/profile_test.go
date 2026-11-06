package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/profile"
)

const cliTestProfile = "../../testdata/profile"

// relaxedProfile copies the test profile and gives it every relaxing option.
func relaxedProfile(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "profile")
	if err := os.CopyFS(dst, os.DirFS(cliTestProfile)); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(dst, "PROFILE.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(string(doc), "example: main.tf outputs.tf versions.tf README.md\n", "example:\n", 1)
	s = strings.Replace(s, "example-dirs: examples/basic examples/complete\n", "example-dirs:\n", 1)
	s += "\n```profile-constants\nname: profile-options\nformat: 1\nconsumers: in-repo\npartitions: fixed\nexamples: none\nrelease: none\n```\n"
	if err := os.WriteFile(filepath.Join(dst, "PROFILE.md"), []byte(s), 0o644); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	return dst
}

type checkOut struct {
	Valid        bool             `json:"valid"`
	Problems     []string         `json:"problems"`
	Options      *profile.Options `json:"options"`
	Relaxing     bool             `json:"relaxing"`
	Gates        []profile.Gate   `json:"gates"`
	GatedRules   []string         `json:"gated_rules"`
	DowngradeIDs []string         `json:"downgrade_ids"`
}

func runProfileCheck(t *testing.T, args ...string) (int, checkOut, string) {
	t.Helper()
	code, out, errOut := runCLI(append([]string{"profile-check"}, args...))
	var c checkOut
	if out != "" {
		if err := json.Unmarshal([]byte(out), &c); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, out)
		}
	}
	return code, c, errOut
}

func TestProfileCheck(t *testing.T) {
	code, c, _ := runProfileCheck(t, "--profile-dir", cliTestProfile)
	if code != 0 || !c.Valid || c.Options == nil || *c.Options != profile.DefaultOptions || c.Relaxing || c.Gates != nil || c.DowngradeIDs != nil {
		t.Errorf("default profile: exit %d, %+v", code, c)
	}
	code, c, _ = runProfileCheck(t, "--profile-dir", relaxedProfile(t), "--gates", "--downgrade-ids")
	if code != 0 || !c.Valid || !c.Relaxing {
		t.Fatalf("relaxed profile: exit %d, %+v", code, c)
	}
	if !slices.EqualFunc(c.Gates, profile.Gates, func(a, b profile.Gate) bool {
		return a.Option == b.Option && a.Value == b.Value && slices.Equal(a.Rules, b.Rules)
	}) {
		t.Errorf("gates %+v, want the table", c.Gates)
	}
	if !slices.Equal(c.DowngradeIDs, profile.DowngradeRules) || !slices.Contains(c.GatedRules, "profile.template-drift") || len(c.GatedRules) != 16 {
		t.Errorf("downgrade ids %q, gated rules %q", c.DowngradeIDs, c.GatedRules)
	}
}

func TestProfileCheckInvalid(t *testing.T) {
	dir := relaxedProfile(t)
	doc, _ := os.ReadFile(filepath.Join(dir, "PROFILE.md"))
	bad := strings.Replace(string(doc), "release: none", "release: maybe", 1)
	if err := os.WriteFile(filepath.Join(dir, "PROFILE.md"), []byte(bad), 0o644); err != nil { //nolint:gosec // G703: the test's own temp dir
		t.Fatal(err)
	}
	code, c, _ := runProfileCheck(t, "--profile-dir", dir)
	if code != 1 || c.Valid || len(c.Problems) == 0 || !strings.Contains(c.Problems[0], "release") {
		t.Errorf("exit %d, %+v; want 1 with the problem", code, c)
	}
	if strings.Contains(strings.Join(c.Problems, " "), dir) {
		t.Errorf("problems name the host path: %q", c.Problems)
	}
}

// TestProfileCheckRefusesWhatSnapshotRefuses checks profile-check exits 2
// on a tree profile-snapshot would refuse, so a check cannot pass where the
// snapshot later fails.
func TestProfileCheckRefusesWhatSnapshotRefuses(t *testing.T) {
	for name, setup := range map[string]func(dir string) error{
		"symlink":  func(dir string) error { return os.Symlink("PROFILE.md", filepath.Join(dir, "x.md")) },
		"fifo":     func(dir string) error { return syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644) },
		"bad name": func(dir string) error { return os.WriteFile(filepath.Join(dir, "a\nb"), nil, 0o644) },
		"over the byte cap": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "big"), make([]byte, 8<<20+1), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := relaxedProfile(t)
			if err := setup(src); err != nil {
				t.Fatal(err)
			}
			if code, _, _ := runProfileCheck(t, "--profile-dir", src); code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
		})
	}
}

func TestProfileCheckUnreadable(t *testing.T) {
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(cliTestProfile, link); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"missing":  {"--profile-dir", filepath.Join(t.TempDir(), "none")},
		"link":     {"--profile-dir", link},
		"no flag":  {},
		"argument": {"--profile-dir", cliTestProfile, "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := runProfileCheck(t, args...); code != 2 {
				t.Errorf("exit %d, want 2", code)
			}
		})
	}
}

type snapOut struct {
	Dir           string  `json:"dir"`
	TreeSHA256    string  `json:"tree_sha256"`
	ProfileSHA256 *string `json:"profile_sha256"`
}

func TestProfileSnapshot(t *testing.T) {
	src := relaxedProfile(t)
	code, out, errOut := runCLI([]string{"profile-snapshot", "--profile-dir", src})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var s snapOut
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(s.Dir) })
	if s.ProfileSHA256 == nil || s.TreeSHA256 == "" {
		t.Fatalf("snapshot %+v", s)
	}
	// The snapshot checks as the source does, and lint reads the same hashes
	// from the source and from the snapshot.
	if code, c, _ := runProfileCheck(t, "--profile-dir", s.Dir, "--gates"); code != 0 || !c.Relaxing {
		t.Errorf("profile-check of the snapshot: exit %d, %+v", code, c)
	}
	head := t.TempDir()
	if err := os.WriteFile(filepath.Join(head, "main.tf"), []byte("variable \"a\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{src, s.Dir} {
		code, out, errOut := runCLI([]string{"lint", head, "--json", "--profile-dir", dir})
		if code > 1 && code != 3 {
			t.Fatalf("lint exit %d: %s", code, errOut)
		}
		var r struct {
			Envelope struct {
				ProfileSHA256     *string `json:"profile_sha256"`
				ProfileTreeSHA256 *string `json:"profile_tree_sha256"`
			} `json:"envelope"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if r.Envelope.ProfileSHA256 == nil || *r.Envelope.ProfileSHA256 != *s.ProfileSHA256 ||
			r.Envelope.ProfileTreeSHA256 == nil || *r.Envelope.ProfileTreeSHA256 != s.TreeSHA256 {
			t.Errorf("lint of %s: envelope %v %v, snapshot %v %s", dir, r.Envelope.ProfileSHA256, r.Envelope.ProfileTreeSHA256, *s.ProfileSHA256, s.TreeSHA256)
		}
	}
}

func TestProfileSnapshotRefuses(t *testing.T) {
	for name, setup := range map[string]func(dir string) error{
		"symlink": func(dir string) error { return os.Symlink("PROFILE.md", filepath.Join(dir, "x.md")) },
		"fifo":    func(dir string) error { return syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644) },
		"over the byte cap": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "big"), make([]byte, 8<<20+1), 0o644)
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := relaxedProfile(t)
			if err := setup(src); err != nil {
				t.Fatal(err)
			}
			done := make(chan int, 1)
			go func() {
				code, out, _ := runCLI([]string{"profile-snapshot", "--profile-dir", src})
				if out != "" {
					t.Errorf("stdout %q on a refusal", out)
				}
				done <- code
			}()
			select {
			case code := <-done:
				if code != 2 {
					t.Errorf("exit %d, want 2", code)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("profile-snapshot hangs")
			}
		})
	}
}
