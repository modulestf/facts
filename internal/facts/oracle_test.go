package facts

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// oracleRoot holds the curated oracle fixtures. Each one has head/, an
// optional base/ and expected.json, which lists every rule of the registry,
// implemented or not. See its README for the layout and the conventions.
const oracleRoot = "../../testdata/oracle"

// testProfile is the profile every oracle fixture runs with unless it has a
// profile/ of its own, as a review run always passes one.
const testProfile = "../../testdata/profile"

// oracleFixtures returns the fixture directories under oracleRoot.
func oracleFixtures(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(oracleRoot, "*", "expected.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no oracle fixtures: %v", err)
	}
	dirs := make([]string, len(files))
	for i, f := range files {
		dirs[i] = filepath.Dir(f)
	}
	return dirs
}

func readOracle(t *testing.T, dir string) goldenView {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v goldenView
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s/expected.json: %v", dir, err)
	}
	return v
}

// checkOracleLink refuses a link that the fixture layout does not allow: a
// link outside head/ or base/, a link path with a . or .. component, an
// absolute target, or a target that resolves, from the link's directory,
// outside the fixture.
func checkOracleLink(link, target string) error {
	if !strings.HasPrefix(link, "head/") && !strings.HasPrefix(link, "base/") {
		return fmt.Errorf("link outside head/ or base/: %s", link)
	}
	for c := range strings.SplitSeq(link, "/") {
		if c == "" || c == "." || c == ".." {
			return fmt.Errorf("unsafe link path: %s", link)
		}
	}
	if path.IsAbs(target) {
		return fmt.Errorf("absolute link target: %s", target)
	}
	if r := path.Join(path.Dir(link), target); r == ".." || strings.HasPrefix(r, "../") {
		return fmt.Errorf("link target leaves the fixture: %s -> %s", link, target)
	}
	return nil
}

// setupOracle copies the fixture in src to a temp dir and creates what the
// fixture cannot commit as such: an empty base/ for an empty-base marker, the
// links symlinks.txt lists, and .gitattributes files stored as gitattributes.
func setupOracle(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "empty-base")); err == nil {
		if err := os.Mkdir(filepath.Join(dst, "base"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if f, err := os.Open(filepath.Join(dst, "symlinks.txt")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) == 0 {
				continue
			}
			if len(fields) != 2 {
				t.Fatalf("%s/symlinks.txt: want <link> <target>: %q", src, sc.Text())
			}
			link, target := fields[0], fields[1]
			if err := checkOracleLink(link, target); err != nil {
				t.Fatalf("%s: %v", src, err)
			}
			full := filepath.Join(dst, filepath.FromSlash(link))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.FromSlash(target), full); err != nil {
				t.Fatal(err)
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, tree := range []string{"head", "base"} {
		var renames []string
		err := filepath.WalkDir(filepath.Join(dst, tree), func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && p == filepath.Join(dst, tree) {
				return filepath.SkipDir
			}
			if err == nil && d.Type().IsRegular() && d.Name() == "gitattributes" {
				renames = append(renames, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range renames {
			if err := os.Rename(p, filepath.Join(filepath.Dir(p), ".gitattributes")); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dst
}

// runOracle runs the fixture set up in dir with the inputs it has.
func runOracle(t *testing.T, dir string) goldenView {
	t.Helper()
	o := Options{Head: filepath.Join(dir, "head"), Limits: DefaultLimits}
	if st, err := os.Stat(filepath.Join(dir, "base")); err == nil && st.IsDir() {
		o.Base = filepath.Join(dir, "base")
	}
	if _, err := os.Stat(filepath.Join(dir, "schema-handoff.json")); err == nil {
		o.SchemaHandoff = filepath.Join(dir, "schema-handoff.json")
	}
	o.ProfileDir = testProfile
	if st, err := os.Stat(filepath.Join(dir, "profile")); err == nil && st.IsDir() {
		o.ProfileDir = filepath.Join(dir, "profile")
	}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return goldenView{Manifest: r.Manifest, Findings: r.Findings, Candidates: r.Candidates}
}

func lineKey(l *int) string {
	if l == nil {
		return "null"
	}
	return strconv.Itoa(*l)
}

// oracleKeys renders the compared fields of the implemented rules' results,
// sorted: {rule_id, area, status, reason_code} of a manifest entry, {rule_id, file, line,
// severity} of a finding and {rule_id, file, line, subject} of a candidate.
// Text, evidence, ids and the reason sentence are curated, not compared.
func oracleKeys(v goldenView, implemented func(string) bool) []string {
	var out []string
	for _, e := range v.Manifest {
		if implemented(e.RuleID) {
			out = append(out, strings.Join([]string{"manifest", e.RuleID, e.Area, e.Status, e.ReasonCode}, " "))
		}
	}
	for _, f := range v.Findings {
		if implemented(f.RuleID) {
			out = append(out, strings.Join([]string{"finding", f.RuleID, f.File, lineKey(f.Line), f.Severity}, " "))
		}
	}
	for _, c := range v.Candidates {
		if implemented(c.RuleID) {
			out = append(out, strings.Join([]string{"candidate", c.RuleID, c.File, lineKey(c.Line), c.Subject}, " "))
		}
	}
	slices.Sort(out)
	return out
}

// oracleDiff lists the keys only the oracle has, as "-", and the keys only
// the run has, as "+".
func oracleDiff(want, got goldenView) []string {
	implemented := func(id string) bool {
		return slices.ContainsFunc(Rules, func(r Rule) bool { return r.ID == id })
	}
	w, g := oracleKeys(want, implemented), oracleKeys(got, implemented)
	var diff []string
	for _, k := range w {
		if !slices.Contains(g, k) {
			diff = append(diff, "- "+k)
		}
	}
	for _, k := range g {
		if !slices.Contains(w, k) {
			diff = append(diff, "+ "+k)
		}
	}
	if len(diff) == 0 && !slices.Equal(w, g) {
		diff = append(diff, "duplicate keys differ")
	}
	return diff
}

// TestOracle runs every oracle fixture and compares the implemented rules
// with its expected.json. A rule that is not implemented yet is skipped, so
// the oracle files stay as curated while rules land.
func TestOracle(t *testing.T) {
	for _, src := range oracleFixtures(t) {
		t.Run(filepath.Base(src), func(t *testing.T) {
			dir := setupOracle(t, src)
			if d := oracleDiff(readOracle(t, src), runOracle(t, dir)); len(d) > 0 {
				t.Errorf("run differs from %s/expected.json:\n%s", src, strings.Join(d, "\n"))
			}
		})
	}
}

// TestOracleMutationFails changes one compared field of an oracle at a time
// and checks the comparison sees it, and that a change to a rule that is not
// implemented is not compared.
func TestOracleMutationFails(t *testing.T) {
	src := filepath.Join(oracleRoot, "structure-positive")
	got := runOracle(t, setupOracle(t, src))
	want := readOracle(t, src)
	if len(want.Findings) == 0 || len(want.Candidates) == 0 {
		t.Fatal("structure-positive has no finding or candidate to mutate")
	}
	mi := slices.IndexFunc(want.Manifest, func(e ManifestEntry) bool { return e.RuleID == "structure.missing-description" })
	fi := slices.IndexFunc(want.Findings, func(f Finding) bool { return f.RuleID == "structure.missing-description" })
	ci := slices.IndexFunc(want.Candidates, func(c Candidate) bool { return c.RuleID == "structure.variable-untyped" })
	if mi < 0 || fi < 0 || ci < 0 {
		t.Fatal("structure-positive lacks an implemented rule's entry, finding or candidate")
	}
	clone := func() goldenView {
		return goldenView{Manifest: slices.Clone(want.Manifest), Findings: slices.Clone(want.Findings), Candidates: slices.Clone(want.Candidates)}
	}
	other := 9999
	for name, mutate := range map[string]func(v *goldenView){
		"manifest status":      func(v *goldenView) { v.Manifest[mi].Status = "not_evaluated" },
		"manifest area":        func(v *goldenView) { v.Manifest[mi].Area = "modules/none" },
		"manifest reason code": func(v *goldenView) { v.Manifest[mi].ReasonCode = ReasonParseError },
		"manifest dropped":     func(v *goldenView) { v.Manifest = slices.Delete(v.Manifest, mi, mi+1) },
		"finding line":         func(v *goldenView) { v.Findings[fi].Line = &other },
		"finding file":         func(v *goldenView) { v.Findings[fi].File = "other.tf" },
		"finding severity":     func(v *goldenView) { v.Findings[fi].Severity = "HIGH" },
		"finding dropped":      func(v *goldenView) { v.Findings = slices.Delete(v.Findings, fi, fi+1) },
		"finding added":        func(v *goldenView) { v.Findings = append(v.Findings, v.Findings[fi]) },
		"candidate subject": func(v *goldenView) {
			v.Candidates[ci].Subject = ".:var.other"
		},
		"candidate line":    func(v *goldenView) { v.Candidates[ci].Line = nil },
		"candidate dropped": func(v *goldenView) { v.Candidates = slices.Delete(v.Candidates, ci, ci+1) },
	} {
		v := clone()
		mutate(&v)
		if len(oracleDiff(v, got)) == 0 {
			t.Errorf("%s: mutated oracle still matches", name)
		}
	}
	// Control: the unmutated oracle matches, and a rule outside Rules is not compared.
	if d := oracleDiff(want, got); len(d) > 0 {
		t.Fatalf("unmutated oracle differs: %v", d)
	}
	v := clone()
	v.Manifest = append(v.Manifest, ManifestEntry{RuleID: "zz.not-implemented", Area: ".", Status: "clean"})
	if d := oracleDiff(v, got); len(d) > 0 {
		t.Errorf("a rule outside Rules was compared: %v", d)
	}
}

func TestCheckOracleLink(t *testing.T) {
	for _, c := range []struct {
		link, target string
		ok           bool
	}{
		{"head/linked.tf", "../outside.tf", true},
		{"head/modules/alias", "child", true},
		{"head/examples/complete/versions.tf", "../../versions.tf", true},
		{"other/x.tf", "y.tf", false},
		{"head/../x.tf", "y.tf", false},
		{"head/./x.tf", "y.tf", false},
		{"head//x.tf", "y.tf", false},
		{"head/x.tf", "/etc/passwd", false},
		{"head/x.tf", "../../outside.tf", false},
		{"head/a/x.tf", "../../../x.tf", false},
	} {
		if err := checkOracleLink(c.link, c.target); (err == nil) != c.ok {
			t.Errorf("%s -> %s: err %v, want ok %v", c.link, c.target, err, c.ok)
		}
	}
}
