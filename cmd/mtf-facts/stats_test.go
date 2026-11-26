package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/stats"
)

// statsCases are runs of every command that takes --stats-file: reports,
// findings, not-evaluated exits and errors, so each exit path is covered.
func statsCases(t *testing.T) map[string][]string {
	t.Helper()
	fb := filepath.Join(fixtureBuildTD, "crafted-01-expr-not-string")
	fbArgs := []string{"fixture-build", filepath.Join(fb, "trigger.json"), "--template", filepath.Join(fixtureBuildTD, "templates", "l1.tf.tmpl"), "--out", t.TempDir()}
	const ex = "../../schema/examples"
	shas := []string{"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)}
	sd := "../../testdata/schema-diff"
	db := "../../testdata/diff-base"
	return map[string][]string{
		"lint text":              {"lint", "../../testdata/variable-untyped-base/head"},
		"lint json base":         {"lint", db + "/head", "--base", db + "/base", "--json"},
		"lint ai":                {"lint", db + "/head", "--diff", db + "/files.json", "--json", "--ai"},
		"lint missing tree":      {"lint", "../../testdata/no-such-tree"},
		"lint bad timeout":       {"lint", db + "/head", "--timeout", "-1s"},
		"affected json":          {"affected", db + "/head", "--base", db + "/base", "--json"},
		"affected text":          {"affected", db + "/head", "--base", db + "/base"},
		"behaviour":              {"behaviour", pr556 + "/head", "--base", pr556 + "/base", "--json"},
		"behaviour no json":      {"behaviour", pr556 + "/head", "--base", pr556 + "/base"},
		"claims-eval":            append([]string{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json"}, shas...),
		"claims-eval no ladder":  {"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--json"},
		"depth":                  {"depth", "--head", db + "/head", "--base", db + "/base", "--files", db + "/files.json"},
		"depth missing":          {"depth", "--head", db + "/head"},
		"fixture-check":          {"fixture-check", "../../testdata/fixture-check/l1", "--json"},
		"fixture-check text":     {"fixture-check", "../../testdata/fixture-check/l1"},
		"schema-diff":            {"schema-diff", "--from", filepath.Join(sd, "from.json"), "--to", filepath.Join(sd, "to.json"), "--types-from", filepath.Join(sd, "module")},
		"schema-diff no from":    {"schema-diff", "--to", filepath.Join(sd, "to.json"), "--types-from", filepath.Join(sd, "module")},
		"fixture-check bad tree": {"fixture-check", "../../testdata/no-such-tree"},
		"fixture-build refused":  fbArgs, // exit 1, no file, so the same --out serves both runs
		"fixture-build no out":   {"fixture-build", filepath.Join(fb, "trigger.json"), "--template", filepath.Join(fixtureBuildTD, "templates", "l1.tf.tmpl"), "--out", "../../testdata/no-such-dir"},
	}
}

// TestStatsKeepOutput runs each case with and without --stats-file: the
// exit code, stdout and stderr must be byte for byte the same, and the
// record must name the command and its exit code.
func TestStatsKeepOutput(t *testing.T) {
	for name, args := range statsCases(t) {
		t.Run(name, func(t *testing.T) {
			code, out, errOut := runCLI(args)
			p := filepath.Join(t.TempDir(), "stats.json")
			code2, out2, errOut2 := runCLI(append(slices.Clone(args), "--stats-file", p))
			if code2 != code || out2 != out || errOut2 != errOut {
				t.Fatalf("with --stats-file: exit %d, stdout %d bytes, stderr %q; without: exit %d, stdout %d bytes, stderr %q",
					code2, len(out2), errOut2, code, len(out), errOut)
			}
			rec := readStats(t, p)
			if rec.Command != args[0] || rec.ExitCode != code {
				t.Errorf("record of %s exit %d, want %s exit %d", rec.Command, rec.ExitCode, args[0], code)
			}
			want := stats.OutcomeCompleted
			if code == exitError {
				want = stats.OutcomeError
			}
			if rec.Outcome != want {
				t.Errorf("outcome %s, want %s", rec.Outcome, want)
			}
			var sum int64
			for _, ph := range rec.Phases {
				sum += ph.WallNS
			}
			if len(rec.Phases) == 0 || rec.Phases[0].Name != "run" || sum > rec.WallNS {
				t.Errorf("phases %+v sum %d over wall %d", rec.Phases, sum, rec.WallNS)
			}
		})
	}
}

// TestStatsLintPhases checks the lint record holds the engine's phases,
// the files it read, the parse total and one entry per rule.
func TestStatsLintPhases(t *testing.T) {
	const db = "../../testdata/diff-base"
	p := filepath.Join(t.TempDir(), "stats.json")
	if code, _, errOut := runCLI([]string{"lint", db + "/head", "--base", db + "/base", "--json", "--stats-file", p}); code == exitError {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	rec := readStats(t, p)
	var names []string
	for _, ph := range rec.Phases {
		names = append(names, ph.Name)
	}
	want := []string{"run", "binary_hash", "read_head", "read_base", "areas", "schema", "profile", "change_map", "rules", "callers", "report", "render"}
	if !slices.Equal(names, want) {
		t.Errorf("phases %v, want %v", names, want)
	}
	if rec.FilesRead == 0 || rec.BytesRead == 0 || rec.Parse.Calls == 0 || rec.Parse.Files == 0 {
		t.Errorf("read %d files %d bytes, parse %+v", rec.FilesRead, rec.BytesRead, rec.Parse)
	}
	if !slices.IsSortedFunc(rec.Rules, func(a, b stats.Rule) int { return strings.Compare(a.ID, b.ID) }) {
		t.Errorf("rules %+v, want them sorted by id", rec.Rules)
	}
	// The change touches an area, so every rule judged per area ran there.
	timed := map[string]bool{}
	for _, r := range rec.Rules {
		timed[r.ID] = r.Calls > 0
	}
	for _, r := range facts.Rules {
		if !r.CrossArea && !timed[r.ID] {
			t.Errorf("rule %s has no timed call", r.ID)
		}
	}
	// A wrong ru_maxrss unit is off by 1024: kilobytes read as bytes fall
	// under 1 MiB, bytes read as kilobytes pass 16 GiB.
	if rec.MaxRSSBytes == nil {
		t.Error("max_rss_bytes is null")
	} else if m := *rec.MaxRSSBytes; m < 1<<20 || m > 16<<30 {
		t.Errorf("max_rss_bytes %d, want between 1 MiB and 16 GiB", m)
	}
}

// TestStatsWriteFailure checks a stats file that cannot be written adds
// one line to stderr and changes nothing else.
func TestStatsWriteFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	args := []string{"lint", "../../testdata/variable-untyped-base/head"}
	code, out, errOut := runCLI(args)
	for _, p := range []string{link, dir, filepath.Join(dir, "no", "such.json")} {
		code2, out2, errOut2 := runCLI(append(slices.Clone(args), "--stats-file", p))
		if code2 != code || out2 != out {
			t.Errorf("%s: exit %d or stdout changed", p, code2)
		}
		extra, ok := strings.CutPrefix(errOut2, errOut)
		if !ok || !strings.HasPrefix(extra, "mtf-facts: stats: ") || strings.Count(extra, "\n") != 1 {
			t.Errorf("%s: stderr %q, want %q plus one stats line", p, errOut2, errOut)
		}
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("link target changed to %q", b)
	}
}

// TestStatsTimeout holds each command's worker past --timeout: the record
// says timeout with the exit code of the run, 0 for depth and 2 for the
// others, and the command's own output is what it is without the flag.
func TestStatsTimeout(t *testing.T) {
	testHookWork = func() { select {} } // the held worker leaks until the test binary exits
	t.Cleanup(func() { testHookWork = nil })
	for name, args := range statsCommands(t) {
		t.Run(name, func(t *testing.T) {
			args := append(slices.Clone(args), "--timeout", "20ms")
			code, out, errOut := runCLI(args)
			p := filepath.Join(t.TempDir(), "stats.json")
			code2, out2, errOut2 := runCLI(append(slices.Clone(args), "--stats-file", p))
			if code2 != code || out2 != out || errOut2 != errOut {
				t.Fatalf("with --stats-file: exit %d stderr %q; without: exit %d stderr %q", code2, errOut2, code, errOut)
			}
			want := exitError
			if name == "depth" {
				want = exitClean
			}
			rec := readStats(t, p)
			if code != want || rec.Outcome != stats.OutcomeTimeout || rec.ExitCode != want {
				t.Errorf("exit %d, outcome %s exit %d, want timeout exit %d", code, rec.Outcome, rec.ExitCode, want)
			}
		})
	}
}

// TestStatsEngineDeadline has the engine return the deadline error, as the
// engine's own deadline does when it wins the race with the timer: the
// record says timeout.
func TestStatsEngineDeadline(t *testing.T) {
	orig := runFacts
	runFacts = func(facts.Options) (*facts.Report, error) { return nil, facts.ErrDeadline }
	t.Cleanup(func() { runFacts = orig })
	p := filepath.Join(t.TempDir(), "stats.json")
	code, out, errOut := runCLI([]string{"lint", "../../testdata/diff-base/head", "--stats-file", p})
	if code != exitError || out != "" || errOut != "mtf-facts: timeout\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if rec := readStats(t, p); rec.Outcome != stats.OutcomeTimeout {
		t.Errorf("outcome %s, want timeout", rec.Outcome)
	}
}

// statsCommands is one successful run of each command that takes
// --stats-file.
func statsCommands(t *testing.T) map[string][]string {
	t.Helper()
	const ex = "../../schema/examples"
	sd, db := "../../testdata/schema-diff", "../../testdata/diff-base"
	return map[string][]string{
		"lint":          {"lint", db + "/head", "--base", db + "/base", "--json"},
		"affected":      {"affected", db + "/head", "--base", db + "/base", "--json"},
		"behaviour":     {"behaviour", pr556 + "/head", "--base", pr556 + "/base", "--json"},
		"claims-eval":   {"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json", "--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)},
		"depth":         {"depth", "--head", db + "/head", "--base", db + "/base", "--files", db + "/files.json"},
		"schema-diff":   {"schema-diff", "--from", filepath.Join(sd, "from.json"), "--to", filepath.Join(sd, "to.json"), "--types-from", filepath.Join(sd, "module")},
		"fixture-check": {"fixture-check", "../../testdata/fixture-check/l1", "--json"},
		"fixture-build": fixtureBuildArgs(t, "ladder-pass-01"), // a timeout writes nothing
	}
}

// TestStatsRefusesInputs names an input of the run as the stats file, for
// each command family: a file in a tree argument or a tree flag, a new file
// in such a tree, and a single input file. Each is refused with one stderr
// line, nothing is written and stdout and the exit code stay the same. The
// inputs are copies, so a broken guard cannot touch the fixtures.
func TestStatsRefusesInputs(t *testing.T) {
	tmp := t.TempDir()
	cp := func(src string) string {
		dst := filepath.Join(tmp, strings.NewReplacer("/", "_", ".", "_").Replace(src))
		st, err := os.Stat(src)
		if err != nil {
			t.Fatal(err)
		}
		if st.IsDir() {
			err = os.CopyFS(dst, os.DirFS(src))
		} else {
			var b []byte
			if b, err = os.ReadFile(src); err == nil {
				err = os.WriteFile(dst, b, 0o600) //nolint:gosec // G703: copies a fixture into the test's own temp dir
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		return dst
	}
	const ex = "../../schema/examples"
	sd, db := "../../testdata/schema-diff", "../../testdata/diff-base"
	head, base, files := cp(db+"/head"), cp(db+"/base"), cp(db+"/files.json")
	bh, bb := cp(pr556+"/head"), cp(pr556+"/base")
	beh, lad := cp(ex+"/behaviour.valid.json"), cp(ex+"/ladder-record.valid.json")
	from, to, mod := cp(sd+"/from.json"), cp(sd+"/to.json"), cp(sd+"/module")
	schemaFile := cp(sd + "/to.json")
	fx := cp("../../testdata/fixture-check/l1")
	shas := []string{"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)}
	firstFile := func(dir string) string {
		var found string
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && found == "" && d.Type().IsRegular() {
				found = p
			}
			return err
		})
		if err != nil || found == "" {
			t.Fatalf("no file in %s: %v", dir, err)
		}
		return found
	}
	cases := []struct {
		name, target string
		args         []string
	}{
		{"lint tree argument", firstFile(head), []string{"lint", head, "--base", base}},
		{"lint new file in --base", filepath.Join(base, "stats.json"), []string{"lint", head, "--base", base}},
		{"lint --diff", files, []string{"lint", head, "--diff", files}},
		{"lint --schema-files", schemaFile, []string{"lint", head, "--base", base, "--schema-files", schemaFile}},
		{"affected tree argument", filepath.Join(head, "new.json"), []string{"affected", head, "--base", base}},
		{"behaviour --base", firstFile(bb), []string{"behaviour", bh, "--base", bb, "--json"}},
		{"claims-eval --behaviour", beh, append([]string{"claims-eval", "--behaviour", beh, "--ladder", lad, "--json"}, shas...)},
		{"claims-eval --ladder", lad, append([]string{"claims-eval", "--behaviour", beh, "--ladder", lad, "--json"}, shas...)},
		{"depth --head", firstFile(head), []string{"depth", "--head", head, "--base", base, "--files", files}},
		{"depth --files", files, []string{"depth", "--head", head, "--base", base, "--files", files}},
		{"schema-diff --to", to, []string{"schema-diff", "--from", from, "--to", to, "--types-from", mod}},
		{"schema-diff --types-from", firstFile(mod), []string{"schema-diff", "--from", from, "--to", to, "--types-from", mod}},
		{"fixture-check tree argument", firstFile(fx), []string{"fixture-check", fx, "--json"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, statErr := os.ReadFile(c.target)
			code, out, errOut := runCLI(c.args)
			code2, out2, errOut2 := runCLI(append(slices.Clone(c.args), "--stats-file", c.target))
			if code2 != code || out2 != out {
				t.Errorf("exit %d or stdout changed", code2)
			}
			extra, ok := strings.CutPrefix(errOut2, errOut)
			if !ok || !strings.HasPrefix(extra, "mtf-facts: stats: ") || !strings.Contains(extra, "input") || strings.Count(extra, "\n") != 1 {
				t.Errorf("stderr %q, want %q plus one stats line naming an input", errOut2, errOut)
			}
			after, err := os.ReadFile(c.target)
			switch {
			case statErr == nil && (err != nil || !bytes.Equal(after, before)):
				t.Errorf("%s changed", c.target)
			case statErr != nil && err == nil:
				t.Errorf("%s was created", c.target)
			}
		})
	}
}

// TestStatsRefusesOutputs runs a helper process whose stdout, then stderr,
// goes to a file and names that file as the stats file: it is refused and
// keeps what the run wrote.
func TestStatsRefusesOutputs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"affected", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base"}
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), stream+".txt")
			f, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			//nolint:gosec // G204: re-runs this test binary as a helper process
			cmd := exec.CommandContext(t.Context(), exe, append(slices.Clone(args), "--stats-file", p)...)
			cmd.Env = append(os.Environ(), cliEnv+"=1")
			var other bytes.Buffer
			if stream == "stdout" {
				cmd.Stdout, cmd.Stderr = f, &other
			} else {
				cmd.Stdout, cmd.Stderr = &other, f
			}
			if err := cmd.Run(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if stream == "stdout" {
				if !strings.Contains(got, "inert:") || !strings.Contains(other.String(), "mtf-facts: stats: ") {
					t.Errorf("stdout file %q, stderr %q", got, other.String())
				}
			} else if !strings.HasPrefix(got, "mtf-facts: stats: ") || strings.Contains(got, "schema_version") {
				t.Errorf("stderr file %q, want the refusal and no record", got)
			}
		})
	}
}

// TestStatsDepthDeadline runs depth past its deadline: depth answers with
// the unwalked report and exit 0, and the record says timeout.
func TestStatsDepthDeadline(t *testing.T) {
	const db = "../../testdata/diff-base"
	p := filepath.Join(t.TempDir(), "stats.json")
	args := []string{"depth", "--head", db + "/head", "--base", db + "/base", "--files", db + "/files.json", "--timeout", "1ns"}
	code, out, _ := runCLI(append(args, "--stats-file", p))
	if code != exitClean || !strings.Contains(out, "deep-unwalked") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if rec := readStats(t, p); rec.Outcome != stats.OutcomeTimeout || rec.ExitCode != exitClean {
		t.Errorf("outcome %s exit %d, want timeout exit 0", rec.Outcome, rec.ExitCode)
	}
}

// TestStatsEmptyPath reports an explicit empty --stats-file on stderr and
// changes nothing else.
func TestStatsEmptyPath(t *testing.T) {
	args := []string{"lint", "../../testdata/variable-untyped-base/head"}
	code, out, errOut := runCLI(args)
	code2, out2, errOut2 := runCLI(append(slices.Clone(args), "--stats-file", ""))
	if code2 != code || out2 != out || errOut2 != errOut+"mtf-facts: stats: --stats-file is empty, no record written\n" {
		t.Errorf("exit %d, stderr %q", code2, errOut2)
	}
}

// TestStatsReusesOwnFile checks a second run replaces the record of the
// first at the same path, as a host reusing one temporary path does.
func TestStatsReusesOwnFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "stats.json")
	args := []string{"affected", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--stats-file", p}
	runCLI(args)
	first := readStats(t, p)
	runCLI(args)
	second := readStats(t, p)
	if second.StartedUnixNS <= first.StartedUnixNS {
		t.Errorf("second record started %d, first %d", second.StartedUnixNS, first.StartedUnixNS)
	}
}

func readStats(t *testing.T, p string) stats.Record {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var r stats.Record
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	if r.SchemaVersion != stats.SchemaVersion {
		t.Errorf("schema_version %d", r.SchemaVersion)
	}
	return r
}

// TestStatsRefusesCaseAlias names a file of the input tree through a
// lower-cased directory name, which is the same directory on a
// case-insensitive file system: it is refused and the file is unchanged.
func TestStatsRefusesCaseAlias(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "T")
	if err := os.CopyFS(tree, os.DirFS("../../testdata/variable-untyped-base/head")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "t")
	if _, err := os.Stat(alias); err != nil {
		t.Skip("the temp file system is case-sensitive")
	}
	target := filepath.Join(tree, "variables.tf")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI([]string{"lint", tree, "--json"})
	code2, out2, errOut2 := runCLI([]string{"lint", tree, "--json", "--stats-file", filepath.Join(alias, "variables.tf")})
	if code2 != code || out2 != out || !strings.HasPrefix(strings.TrimPrefix(errOut2, errOut), "mtf-facts: stats: ") {
		t.Errorf("exit %d, stderr %q", code2, errOut2)
	}
	if after, _ := os.ReadFile(target); !bytes.Equal(after, before) {
		t.Errorf("%s changed", target)
	}
}

// TestStatsRefusesDotDot names a file of the input tree as lnk/../file,
// with lnk a link into the tree: the kernel resolves .. after the link, so
// the path is the tree's file, and it is refused. A plain .. path is
// refused too.
func TestStatsRefusesDotDot(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "T")
	if err := os.CopyFS(tree, os.DirFS("../../testdata/variable-untyped-base/head")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tree, "modules"), filepath.Join(dir, "lnk")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tree, "variables.tf")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"lint", tree, "--json"}
	code, out, errOut := runCLI(args)
	for _, p := range []string{dir + "/lnk/../variables.tf", tree + "/../stats.json"} {
		code2, out2, errOut2 := runCLI(append(slices.Clone(args), "--stats-file", p))
		extra, ok := strings.CutPrefix(errOut2, errOut)
		if code2 != code || out2 != out || !ok || !strings.HasPrefix(extra, "mtf-facts: stats: ") {
			t.Errorf("%s: exit %d, stderr %q", p, code2, errOut2)
		}
	}
	if after, _ := os.ReadFile(target); !bytes.Equal(after, before) {
		t.Errorf("%s changed", target)
	}
	if _, err := os.Lstat(filepath.Join(dir, "stats.json")); err == nil {
		t.Error("a record was written through a .. path")
	}
}
