package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const guardSrc = `package p

import "fmt"

func f(xs []string, n int) int {
	for _, x := range xs {
		if x == "" {
			continue
		}
		if len(x) > n && x != "skip" && !ignored(x) {
			return 1
		}
	}
	if v := n * 2; v > 10 {
		panic("big")
	}
	switch {
	case n < 0:
		return -1
	case n == 0 || n == 1:
		n++
	}
	if n > 3 {
		n--
	} else {
		return 0
	}
	return n
}

func ignored(s string) bool { return s == "x" }

// None of these is a guard: an else, a body that does not leave, a switch
// with a tag, a case of two expressions, an empty body, a body ending in an
// expression that is no call, in a call through a selector, or in a call
// of another function.
func notGuards(n int) int {
	if n > 5 {
		return 5
	} else {
		n++
	}
	if n == 7 {
		n++
	}
	switch n {
	case 9:
		return 9
	}
	switch {
	case n == 10, n == 11:
		return 0
	}
	if n == 12 {
	}
	if n == 13 {
		<-make(chan int)
	}
	if n == 14 {
		fmt.Println()
	}
	if n == 15 {
		println()
	}
	return n
}
`

func allLines(src string) map[int]bool {
	m := map[int]bool{}
	for i := range strings.Count(src, "\n") + 1 {
		m[i+1] = true
	}
	return m
}

func TestMutantsKinds(t *testing.T) {
	got, err := mutants("p/f.go", []byte(guardSrc), allLines(guardSrc))
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, m := range got {
		have = append(have, m.Kind+" "+m.Code)
	}
	want := []string{
		kindGuard + ` x == ""`,
		kindGuard + ` len(x) > n && x != "skip" && !ignored(x)`,
		kindOperand + ` len(x) > n`,
		kindOperand + ` x != "skip"`,
		kindOperand + ` !ignored(x)`,
		kindGuard + ` v > 10`,
		kindGuard + ` n < 0`,
		kindOperand + ` n == 0`,
		kindOperand + ` n == 1`,
	}
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Fatalf("mutants:\n%s\nwant:\n%s", strings.Join(have, "\n"), strings.Join(want, "\n"))
	}
}

// Every mutant must still be a valid file: the rewrites keep each
// identifier referenced, so no unused variable or import hides a guard.
func TestMutantsCompile(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{"f.go": guardSrc})
	ms, err := mutants("f.go", []byte(guardSrc), allLines(guardSrc))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if err := os.WriteFile(filepath.Join(dir, "f.go"), m.Src, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s at line %d does not build: %s\n%s", m.Kind, m.Line, out, m.Src)
		}
	}
}

func TestMutantsOnlyChangedLines(t *testing.T) {
	// Line 10 holds the && chain; only it is changed.
	got, err := mutants("p/f.go", []byte(guardSrc), map[int]bool{10: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d mutants, want the guard and its three operands", len(got))
	}
	for _, m := range got {
		if m.Line != 10 {
			t.Errorf("mutant at line %d outside the change", m.Line)
		}
	}
	none, err := mutants("p/f.go", []byte(guardSrc), map[int]bool{11: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("a change in the body made %d mutants of its guard", len(none))
	}
}

func TestMutantsMultilineCondition(t *testing.T) {
	src := "package p\n\nfunc g(a, b bool) int {\n\tif a &&\n\t\tb {\n\t\treturn 1\n\t}\n\treturn 0\n}\n"
	got, err := mutants("p/g.go", []byte(src), map[int]bool{5: true})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, m := range got {
		kinds = append(kinds, m.Kind+" "+m.Code)
	}
	want := []string{kindGuard + " a && b", kindOperand + " b"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("got %q, want %q", kinds, want)
	}
}

func TestChangedLines(t *testing.T) {
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -3,0 +4,2 @@ func x() {
+	a
+	b
@@ -10 +12 @@
-	c
+	d
@@ -20,3 +23,0 @@
-	gone
diff --git a/b_test.go b/b_test.go
--- a/b_test.go
+++ b/b_test.go
@@ -1 +1 @@
-x
+y
diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1 +0,0 @@
-x
diff --git a/notes.txt b/notes.txt
--- a/notes.txt
+++ b/notes.txt
@@ -1 +1 @@
-x
+y
diff --git a/testdata/t.go b/testdata/t.go
--- a/testdata/t.go
+++ b/testdata/t.go
@@ -1 +1 @@
-x
+y
diff --git "a/sp ace.go" "b/sp ace.go"
--- "a/sp ace.go"
+++ "b/sp ace.go"
@@ -1 +1 @@
-x
+y
diff --git a/my pkg/c.go b/my pkg/c.go
--- a/my pkg/c.go	
+++ b/my pkg/c.go	
@@ -1 +3 @@
-x
+y
`
	got, err := changedLines(strings.NewReader(diff))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["my pkg/c.go"][3] {
		t.Fatalf("files %v, want a.go and line 3 of the file whose name holds a space", got)
	}
	want := map[int]bool{4: true, 5: true, 12: true}
	if len(got["a.go"]) != len(want) {
		t.Fatalf("a.go lines %v, want %v", got["a.go"], want)
	}
	for l := range want {
		if !got["a.go"][l] {
			t.Errorf("line %d missing", l)
		}
	}
}

func TestChangedLinesBadHunk(t *testing.T) {
	_, err := changedLines(strings.NewReader("+++ b/a.go\n@@ -1 +x @@\n"))
	if err == nil {
		t.Fatal("a malformed hunk header was accepted")
	}
}

func writeModule(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	files["go.mod"] = "module example.com/m\n\ngo 1.22\n"
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A guard the tests exercise is killed; one they never reach survives; a
// change outside it leaves the checkout untouched.
func TestRunEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	src := `package m

func Pos(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func Clean(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
`
	test := `package m

import "testing"

func TestPos(t *testing.T) {
	if Pos(-1) != 0 {
		t.Fatal("neg")
	}
}

func TestClean(t *testing.T) {
	if Clean("a") != "a" {
		t.Fatal("a")
	}
}
`
	writeModule(t, dir, map[string]string{"m.go": src, "m_test.go": test})
	ms, err := mutants("m.go", []byte(src), allLines(src))
	if err != nil {
		t.Fatal(err)
	}
	res := run(context.Background(), dir, ms, runOptions{workers: 2, budget: 2 * time.Minute})
	status := map[int]string{}
	for _, r := range res {
		status[r.Line] = r.Status
	}
	if status[4] != statusKilled || status[11] != statusSurvived {
		t.Fatalf("statuses %v, want line 4 killed and line 11 survived", status)
	}
	after, err := os.ReadFile(filepath.Join(dir, "m.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != src {
		t.Fatal("the run changed the checkout")
	}
}

func TestRunBaselineFails(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	src := "package m\n\nfunc F(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n"
	writeModule(t, dir, map[string]string{
		"m.go":      src,
		"m_test.go": "package m\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { t.Fatal(\"broken\") }\n",
	})
	ms, err := mutants("m.go", []byte(src), allLines(src))
	if err != nil {
		t.Fatal(err)
	}
	res := run(context.Background(), dir, ms, runOptions{workers: 1, budget: time.Minute})
	if len(res) != 1 || res[0].Status != statusBaseline {
		t.Fatalf("results %+v, want one baseline failure", res)
	}
}

func TestRunBudgetSpent(t *testing.T) {
	dir := t.TempDir()
	src := "package m\n\nfunc F(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n"
	writeModule(t, dir, map[string]string{"m.go": src})
	ms, err := mutants("m.go", []byte(src), allLines(src))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := run(ctx, dir, ms, runOptions{workers: 1, budget: time.Minute})
	if len(res) != 1 || res[0].Status != statusNotRun {
		t.Fatalf("results %+v, want one mutant not run", res)
	}
}

func TestReport(t *testing.T) {
	res := []result{
		{File: "a.go", Line: 3, Kind: kindGuard, Code: "x | `y`", Status: statusSurvived},
		{File: "a.go", Line: 1, Kind: kindOperand, Code: "z", Status: statusKilled},
		{File: "b.go", Line: 9, Kind: kindGuard, Code: "w", Status: statusNotRun},
		{File: "c.go", Line: 2, Kind: kindGuard, Code: "v", Status: statusNotCompiled},
		{File: "d.go", Line: 4, Kind: kindGuard, Code: "u", Status: statusNoTests},
	}
	var b strings.Builder
	report(&b, res)
	out := b.String()
	for _, want := range []string{
		"5 mutants on the changed lines: 1 survived, 1 killed, 1 not compiled, 1 not run, 1 no test files.",
		"| `a.go:3` | guard never fires | `x \\| 'y'` |",
		"- `b.go:9` guard never fires: not run",
		"- `d.go:4` guard never fires: no test files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "a.go:3"); n != 1 {
		t.Errorf("the survivor appears %d times, want once in the table:\n%s", n, out)
	}
	for _, gone := range []string{"a.go:1", "c.go:2"} {
		if strings.Contains(out, gone) {
			t.Errorf("report lists %s, which has a verdict:\n%s", gone, out)
		}
	}
	b.Reset()
	report(&b, nil)
	if !strings.Contains(b.String(), "No guard or condition operand on the changed lines.") {
		t.Errorf("empty report: %s", b.String())
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"-base", "x", "extra"},
		{"-base", "x", "-workers", "0"},
		{"-base", "x", "-budget", "0s"},
	} {
		if err := mainErr(context.Background(), args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("%q: got %v, want the usage error", args, err)
		}
	}
}

func TestMutantsParseError(t *testing.T) {
	if _, err := mutants("p/bad.go", []byte("package p\nfunc {"), map[int]bool{2: true}); err == nil {
		t.Fatal("a file that does not parse gave no error")
	}
}

// A //line directive moves the position a parser reports, not the line of the
// file the diff counts.
func TestMutantsIgnoreLineDirective(t *testing.T) {
	src := "package p\n\nfunc f(n int) int {\n//line x.go:500\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n"
	got, err := mutants("p/f.go", []byte(src), map[int]bool{5: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Line != 5 {
		t.Fatalf("mutants %+v, want the guard at line 5 of the file", got)
	}
}

// A package with no test file passes go test, which proves nothing: it has
// no verdict.
func TestRunNoTestFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	src := "package m\n\nfunc F(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n"
	writeModule(t, dir, map[string]string{"m.go": src})
	ms, err := mutants("m.go", []byte(src), allLines(src))
	if err != nil {
		t.Fatal(err)
	}
	res := run(context.Background(), dir, ms, runOptions{workers: 1, budget: time.Minute})
	if len(res) != 1 || res[0].Status != statusNoTests {
		t.Fatalf("results %+v, want one mutant with no test files", res)
	}
}

const slowSrc = "package m\n\nfunc Pos(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n"

// The unmutated code passes at once; the mutant never fires its guard, so
// the test records its process id and then sleeps.
const slowTest = `package m

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPos(t *testing.T) {
	if Pos(-1) != 0 {
		_ = os.WriteFile(os.Getenv("MUTATE_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(10 * time.Minute)
	}
}
`

// The budget ends while a mutant's tests run: the mutant is not run, and the
// test binary is gone, not left behind by the go command that started it.
func TestRunBudgetEndsMidMutant(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": slowTest})
	ms, err := mutants("m.go", []byte(slowSrc), allLines(slowSrc))
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("MUTATE_PIDFILE", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan []result, 1)
	go func() { done <- run(ctx, dir, ms, runOptions{workers: 1, budget: 10 * time.Minute}) }()

	pid := waitForPID(t, pidFile)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	cancel() // the budget's end, as run sees it
	select {
	case res := <-done:
		if len(res) != 1 || res[0].Status != statusNotRun {
			t.Fatalf("results %+v, want one mutant not run", res)
		}
	case <-time.After(time.Minute):
		t.Fatal("run did not return after its context ended")
	}
	assertGone(t, pid)
}

func waitForPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the mutant's test never started")
	return 0
}

// goTest ends a test that outlives the timeout it is given; the baseline run
// gets the rest of the budget as that timeout.
func TestGoTestTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{
		"m.go":      "package m\n",
		"m_test.go": "package m\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestSlow(t *testing.T) { time.Sleep(time.Minute) }\n",
	})
	out, err := goTest(context.Background(), dir, ".", "", 2*time.Second)
	if err == nil || !strings.Contains(string(out), "panic: test timed out") {
		t.Fatalf("err %v, output %s; want a timed out test", err, out)
	}
}

func TestRemaining(t *testing.T) {
	if d := remaining(context.Background()); d != 0 {
		t.Errorf("no deadline: %v, want 0", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if d := remaining(ctx); d < 59*time.Minute || d > time.Hour {
		t.Errorf("an hour left: %v", d)
	}
	past, cancelPast := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelPast()
	if d := remaining(past); d <= 0 {
		t.Errorf("a spent deadline gave %v, want a positive timeout", d)
	}
}

// mainErr end to end on a throwaway repository: a survivor in the report, a
// file behind a build constraint and one that does not parse in the skipped
// list, and the summary file appended to, not replaced.
func TestMainErr(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{
		"m.go":       slowSrc,
		"m_test.go":  "package m\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif Pos(-1) != 0 {\n\t\tt.Fatal(\"neg\")\n\t}\n}\n",
		"ign.go":     "//go:build ignore\n\npackage m\n\nfunc ign() {}\n",
		"bad/bad.go": "package bad\n\nfunc bad() {}\n",
	})
	gitIn := func(args ...string) {
		t.Helper()
		//nolint:gosec // G204: fixed git arguments of a throwaway repository
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q")
	gitIn("add", ".")
	gitIn("commit", "-q", "-m", "base")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("m.go", slowSrc+"\nfunc Clean(s string) string {\n\tif s == \"\" {\n\t\treturn \"-\"\n\t}\n\treturn s\n}\n")
	write("ign.go", "//go:build ignore\n\npackage m\n\nfunc ign(n int) int {\n\tif n < 0 {\n\t\treturn 0\n\t}\n\treturn n\n}\n")
	write("bad/bad.go", "package bad\n\nfunc bad( {\n")
	t.Chdir(dir)

	summary := filepath.Join(t.TempDir(), "summary.md")
	if err := os.WriteFile(summary, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := mainErr(context.Background(), []string{"-base", "HEAD", "-budget", "3m", "-workers", "1", "-summary", summary}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"1 mutant on the changed lines: 1 survived.",
		"| `m.go:11` | guard never fires |",
		"- `ign.go` not built on this platform",
		"- `bad/bad.go` not parsed:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "earlier\n"+out.String() {
		t.Errorf("summary file:\n%s\nwant the earlier text, then the report:\n%s", got, out.String())
	}
}

// A mutant whose tests run past their timeout is timed out, not killed: the
// test binary's own "test timed out" panic is what says so.
func TestRunMutantTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	test := "package m\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestPos(t *testing.T) {\n\tif Pos(-1) != 0 {\n\t\ttime.Sleep(time.Minute)\n\t}\n}\n"
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": test})
	ms, err := mutants("m.go", []byte(slowSrc), allLines(slowSrc))
	if err != nil {
		t.Fatal(err)
	}
	res := run(context.Background(), dir, ms, runOptions{workers: 1, budget: 5 * time.Minute, floor: 2 * time.Second})
	if len(res) != 1 || res[0].Status != statusTimedOut {
		t.Fatalf("results %+v, want one timed out mutant", res)
	}
}

// A mutant whose test binary never reaches the test timeout, because it
// sleeps in init before the timer starts, is ended by the mutant's own
// deadline and is timed out, not killed.
func TestRunMutantOutlivesDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	test := "package m\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif Pos(-1) != 0 {\n\t\tt.Fatal(\"neg\")\n\t}\n}\n"
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": test})
	hung := "package m\n\nimport \"time\"\n\nfunc init() { time.Sleep(10 * time.Minute) }\n\nfunc Pos(n int) int { return n }\n"
	ms := []Mutant{{File: "m.go", Line: 4, Kind: kindGuard, Code: "x", Src: []byte(hung)}}
	res := run(context.Background(), dir, ms, runOptions{workers: 1, budget: 5 * time.Minute, floor: time.Second})
	if len(res) != 1 || res[0].Status != statusTimedOut {
		t.Fatalf("results %+v, want one timed out mutant", res)
	}
}

// builtHere reads nothing from the disk: the file's bytes are all it needs.
func TestBuiltHereOpensNothing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such")
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"a.go", "package m\n", true},
		{"a.go", "//go:build ignore\n\npackage m\n", false},
		{"a_plan9_arm.go", "package m\n", false},
	} {
		got, err := builtHere(missing, tc.name, []byte(tc.src))
		if err != nil || got != tc.want {
			t.Errorf("%s %q: got %v, %v; want %v", tc.name, tc.src, got, err, tc.want)
		}
	}
}

// A mutant that does not build, or whose package cannot be set up, is not compiled, not killed: no
// test failed, none ran.
func TestRunMutantNotCompiled(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	test := "package m\n\nimport \"testing\"\n\nfunc TestPos(t *testing.T) {\n\tif Pos(-1) != 0 {\n\t\tt.Fatal(\"neg\")\n\t}\n}\n"
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": test})
	broken := map[int]string{
		4: strings.Replace(slowSrc, "return n", "return undefined", 1),                           // [build failed]
		7: "package m\n\nimport _ \"example.com/nothere\"\n\nfunc Pos(n int) int { return n }\n", // [setup failed]
	}
	var ms []Mutant
	for line, src := range broken {
		ms = append(ms, Mutant{File: "m.go", Line: line, Kind: kindGuard, Code: "x", Src: []byte(src)})
	}
	res := run(context.Background(), dir, ms, runOptions{workers: 2, budget: 5 * time.Minute})
	if len(res) != 2 {
		t.Fatalf("results %+v", res)
	}
	for _, r := range res {
		if r.Status != statusNotCompiled {
			t.Errorf("line %d: %s, want %s", r.Line, r.Status, statusNotCompiled)
		}
	}
}

const baselineSlowTest = `package m

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPos(t *testing.T) {
	_ = os.WriteFile(os.Getenv("MUTATE_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	time.Sleep(10 * time.Minute)
}
`

// The budget ends while a package's unmutated run is going: its mutants are
// not run, and the unfinished run is not a failed baseline.
func TestRunBudgetEndsDuringBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	writeModule(t, dir, map[string]string{"m.go": slowSrc, "m_test.go": baselineSlowTest})
	ms, err := mutants("m.go", []byte(slowSrc), allLines(slowSrc))
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	t.Setenv("MUTATE_PIDFILE", pidFile)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan []result, 1)
	go func() { done <- run(ctx, dir, ms, runOptions{workers: 1, budget: 10 * time.Minute}) }()

	pid := waitForPID(t, pidFile)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	cancel()
	select {
	case res := <-done:
		if len(res) != 1 || res[0].Status != statusNotRun {
			t.Fatalf("results %+v, want one mutant not run", res)
		}
	case <-time.After(time.Minute):
		t.Fatal("run did not return after its context ended")
	}
	assertGone(t, pid)
}

// File names and error messages are text a change controls: none may close
// a code span, split a table cell or start a link in the report.
func TestReportFileNamesInert(t *testing.T) {
	name := "a`[x](evil.invalid)`b|c.go"
	var b strings.Builder
	report(&b, []result{
		{File: name, Line: 3, Kind: kindGuard, Code: "x", Status: statusSurvived},
		{File: name, Line: 4, Kind: kindGuard, Code: "x", Status: statusNotRun},
	})
	for line := range strings.SplitSeq(b.String(), "\n") {
		if strings.Contains(line, name) || strings.Contains(line, "`[x]") || strings.Contains(strings.ReplaceAll(line, `\|`, ""), "b|c") {
			t.Errorf("a file name reaches the report raw: %q", line)
		}
	}
	if !strings.Contains(b.String(), "a'[x](evil.invalid)'b\\|c.go:3") {
		t.Errorf("the survivor's name is not in the table:\n%s", b.String())
	}
}

// A line break in a name or code must not split a table row or a list item.
func TestReportLineBreaksInert(t *testing.T) {
	var b strings.Builder
	report(&b, []result{
		{File: "a\nINJECT1.go", Line: 3, Kind: kindGuard, Code: "x\r\nINJECT2", Status: statusSurvived},
		{File: "b\rINJECT3.go", Line: 4, Kind: kindGuard, Code: "x", Status: statusNotRun},
	})
	out := b.String()
	if strings.Contains(out, "\r") {
		t.Errorf("a carriage return reaches the report: %q", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		for _, frag := range []string{"INJECT1", "INJECT2", "INJECT3"} {
			if strings.HasPrefix(line, frag) {
				t.Errorf("%s starts a line of its own: %q", frag, line)
			}
		}
	}
	for _, want := range []string{"| `a INJECT1.go:3` |", "`x  INJECT2` |", "- `b INJECT3.go:4`"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestCollectNotesInert(t *testing.T) {
	root := t.TempDir()
	hostile := "a`[x](evil.invalid)`b|c.go"
	if err := os.WriteFile(filepath.Join(root, hostile), []byte("//go:build ignore\n\npackage m\n"), 0o600); err != nil {
		t.Skipf("the file system takes no such name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bad`x`.go"), []byte("package m\nfunc {"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(mustNotes(t, root, hostile, "bad`x`.go", "gone`y`.go", "gone\nINJECT.go", "gone\rINJECT2.go"), "\n")
	for _, raw := range []string{"`[x]", "`x`", "`y`", "b|c"} {
		if strings.Contains(strings.ReplaceAll(notes, `\|`, ""), raw) {
			t.Errorf("notes hold %q raw:\n%s", raw, notes)
		}
	}
	if strings.Contains(notes, "\r") {
		t.Errorf("a carriage return reaches the notes: %q", notes)
	}
	for line := range strings.SplitSeq(notes, "\n") {
		if !strings.HasPrefix(line, "- ") {
			t.Errorf("a line break split a note: %q", line)
		}
	}
	for _, want := range []string{"not built on this platform", "not parsed", "not read"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
}

func mustNotes(t *testing.T, root string, names ...string) []string {
	t.Helper()
	changed := map[string]map[int]bool{}
	for _, n := range names {
		changed[n] = map[int]bool{1: true, 2: true}
	}
	ms, notes := collect(root, changed)
	if len(ms) != 0 || len(notes) != len(names) {
		t.Fatalf("mutants %d, notes %q; want no mutant and a note per file", len(ms), notes)
	}
	return notes
}
