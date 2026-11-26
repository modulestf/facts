package gates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oracleDir holds the plan pass's own gate cases: each run of the Perl
// gates in the skills repository's plan pass tests, its file list and its
// output, over the tree it read (trees/<n>, with the directories and links
// git cannot hold listed in cases.json). {run} stands for the run
// directory.
const oracleDir = "../../testdata/plan-gates"

type oracleCase struct {
	Name      string   `json:"name"`
	Tree      int      `json:"tree"`
	Scope     string   `json:"scope"`
	Mode      string   `json:"mode"`
	Root      string   `json:"root"`
	Example   string   `json:"example"`
	Providers []string `json:"providers"`
	Stdin     []string `json:"stdin"`
	Want      string   `json:"want"`
}

type oracle struct {
	Links map[string][][2]string `json:"links"`
	Dirs  map[string][]string    `json:"dirs"`
	Cases []oracleCase           `json:"cases"`
}

func loadOracle(t *testing.T) oracle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(oracleDir, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var o oracle
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// buildTree copies tree n into a fresh run directory and returns its
// physical path.
func buildTree(t *testing.T, o oracle, n int) string {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(tmp, "pr-review.oracle")
	id := twoDigits(n)
	src := filepath.Join(oracleDir, "trees", id)
	if err := os.CopyFS(run, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	for _, d := range o.Dirs[id] {
		if err := os.MkdirAll(filepath.Join(run, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range o.Links[id] {
		if err := os.Symlink(strings.ReplaceAll(l[1], "{run}", run), filepath.Join(run, l[0])); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func twoDigits(n int) string { return fmt.Sprintf("%02d", n) }

// TestOracle runs every recorded case and wants the Perl's output byte for
// byte.
func TestOracle(t *testing.T) {
	o := loadOracle(t)
	if len(o.Cases) < 200 {
		t.Fatalf("%d cases, want the whole recorded set", len(o.Cases))
	}
	runs := map[int]string{}
	for _, c := range o.Cases {
		run, ok := runs[c.Tree]
		if !ok {
			run = buildTree(t, o, c.Tree)
			runs[c.Tree] = run
		}
		sub := func(s string) string { return strings.ReplaceAll(s, "{run}", run) }
		paths := make([]string, len(c.Stdin))
		for i, p := range c.Stdin {
			paths[i] = sub(p)
		}
		got, err := Run(paths, Options{
			Downloaded: c.Scope == "downloaded",
			Runner:     c.Mode == "runner",
			Root:       sub(c.Root),
			Example:    sub(c.Example),
			Providers:  c.Providers,
			FileBytes:  1 << 20,
			TotalBytes: 64 << 20,
		})
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if want := sub(c.Want); string(got) != want {
			t.Errorf("%s:\n got %q\nwant %q", c.Name, got, want)
		}
	}
}
