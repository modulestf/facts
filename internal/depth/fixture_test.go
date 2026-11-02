package depth

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/facts"
)

// A fixture is one text file: header lines "key: value", then sections
// "-- <name> --" whose body runs to the next section. A name starting with
// base/ or head/ is a file of that tree; a JSON-quoted name may hold any
// byte; a name ending in " [link]" is a symbolic link whose target is the
// body's first line. Other sections: files.json, classify.json and
// affected.json replace the computed inputs; want.json is the exact report.
//
// Header keys: kind (must-be-deep, must-not-be-light, must-be-light, bench),
// depth (the expected depth), reason (a reason the report must hold; one or
// more), depth-not (a depth the report must not have), classify and affected (set to "missing" to give none), examples
// (the --examples value).
type fixture struct {
	name     string
	file     string
	header   map[string][]string
	sections map[string]string
	order    []string
}

func loadFixture(t *testing.T, file string) *fixture {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fx := &fixture{name: strings.TrimSuffix(filepath.Base(file), ".txt"), file: file,
		header: map[string][]string{}, sections: map[string]string{}}
	cur := ""
	var body strings.Builder
	flush := func() {
		if cur != "" {
			fx.sections[cur] = body.String()
			fx.order = append(fx.order, cur)
		}
		body.Reset()
	}
	for line := range strings.Lines(string(data)) {
		trim := strings.TrimRight(line, "\n")
		if strings.HasPrefix(trim, "-- ") && strings.HasSuffix(trim, " --") {
			flush()
			cur = strings.TrimSuffix(strings.TrimPrefix(trim, "-- "), " --")
			if strings.HasPrefix(cur, `"`) {
				var s string
				if err := json.Unmarshal([]byte(strings.TrimSuffix(cur, " [link]")), &s); err != nil {
					t.Fatalf("%s: section name %s: %v", file, cur, err)
				}
				if strings.HasSuffix(cur, " [link]") {
					s += " [link]"
				}
				cur = s
			}
			continue
		}
		if cur == "" {
			if k, v, ok := strings.Cut(trim, ": "); ok {
				fx.header[k] = append(fx.header[k], v)
			}
			continue
		}
		body.WriteString(line)
	}
	flush()
	return fx
}

// writeTrees writes base/ and head/ under dir and returns the files list
// of the change between them.
func (fx *fixture) writeTrees(t *testing.T, dir string) []map[string]any {
	t.Helper()
	trees := map[string]map[string]string{"base": {}, "head": {}}
	for _, name := range fx.order {
		rev, rel, ok := strings.Cut(name, "/")
		if !ok || (rev != "base" && rev != "head") {
			continue
		}
		p := filepath.Join(dir, rev, rel)
		link := strings.HasSuffix(rel, " [link]")
		if link {
			p = strings.TrimSuffix(p, " [link]")
			rel = strings.TrimSuffix(rel, " [link]")
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := fx.sections[name]
		if link {
			target := strings.TrimSpace(body)
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
			trees[rev][rel] = "\x00link:" + target
			continue
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		trees[rev][rel] = body
	}
	for _, rev := range []string{"base", "head"} {
		if err := os.MkdirAll(filepath.Join(dir, rev), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var list []map[string]any
	names := map[string]bool{}
	for _, rev := range trees {
		for p := range rev {
			names[p] = true
		}
	}
	for _, p := range slices.Sorted(maps.Keys(names)) {
		b, inB := trees["base"][p]
		h, inH := trees["head"][p]
		if inB && inH && b == h {
			continue
		}
		status := "modified"
		switch {
		case !inB:
			status = "added"
		case !inH:
			status = "removed"
		}
		add, del := lineDiff(lines(b), lines(h))
		list = append(list, map[string]any{"filename": p, "status": status, "additions": add, "deletions": del})
	}
	return list
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.SplitAfter(s, "\n")
}

// lineDiff counts added and removed lines with a longest common subsequence.
func lineDiff(a, b []string) (add, del int) {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return len(b) - dp[0][0], len(a) - dp[0][0]
}

// run writes the fixture's inputs and runs depth on them.
func (fx *fixture) run(t *testing.T) *Report {
	t.Helper()
	dir := t.TempDir()
	list := fx.writeTrees(t, dir)
	head, base := filepath.Join(dir, "head"), filepath.Join(dir, "base")
	write := func(name string, v any) string {
		p := filepath.Join(dir, name)
		data, ok := fx.sections[name]
		if !ok {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			data = string(b)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if list == nil {
		list = []map[string]any{}
	}
	o := Options{Head: head, Base: base, Files: write("files.json", list), Limits: facts.DefaultLimits}
	if fx.one("classify") != "missing" {
		var v any
		if _, given := fx.sections["classify.json"]; !given {
			rep, err := behaviour.Run(behaviour.Options{Head: head, Base: base, Limits: facts.DefaultLimits})
			if err != nil {
				t.Logf("behaviour: %v", err) // the classifier failed: no report
			} else {
				v = rep
			}
		}
		if v != nil || fx.sections["classify.json"] != "" {
			o.Classify = write("classify.json", v)
		}
	}
	if fx.one("affected") != "missing" {
		var v any
		if _, given := fx.sections["affected.json"]; !given {
			rep, err := facts.Affected(facts.AffectedOptions{Head: head, Base: base, Limits: facts.DefaultLimits})
			if err != nil {
				t.Logf("affected: %v", err)
			} else {
				v = rep
			}
		}
		if v != nil || fx.sections["affected.json"] != "" {
			o.Affected = write("affected.json", v)
		}
	}
	if ex := fx.one("examples"); ex != "" {
		o.Examples = strings.Split(ex, ",")
	}
	rep, err := Run(o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func (fx *fixture) one(key string) string {
	if v := fx.header[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// check compares a report with the fixture's expectations. With
// DEPTH_UPDATE=1 a want.json section is rewritten from the report.
func (fx *fixture) check(t *testing.T, rep *Report) {
	t.Helper()
	got, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	switch kind := fx.one("kind"); kind {
	case "must-be-deep":
		if rep.Depth != Deep {
			t.Errorf("must be deep, got %s %v", rep.Depth, rep.Reasons)
		}
	case "must-not-be-light":
		if rep.Depth == Light {
			t.Errorf("must not be light, got %s %v", rep.Depth, rep.Reasons)
		}
	case "must-be-light":
		if rep.Depth != Light {
			t.Errorf("must be light, got %s %v", rep.Depth, rep.Reasons)
		}
	case "bench", "other":
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if d := fx.one("depth"); d != "" && rep.Depth != d {
		t.Errorf("depth %s, want %s; reasons %v", rep.Depth, d, rep.Reasons)
	}
	if d := fx.one("depth-not"); d != "" && rep.Depth == d {
		t.Errorf("depth %s, which the fixture rules out; reasons %v", rep.Depth, rep.Reasons)
	}
	for _, r := range fx.header["reason"] {
		if !slices.Contains(rep.Reasons, r) {
			t.Errorf("reasons %v lack %s", rep.Reasons, r)
		}
	}
	want, ok := fx.sections["want.json"]
	if !ok {
		return
	}
	if os.Getenv("DEPTH_UPDATE") == "1" {
		fx.update(t, string(got)+"\n")
		return
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatalf("want.json: %v", err)
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if !bytes.Equal(ga, gb) {
		t.Errorf("report differs from want.json; got:\n%s", got)
	}
}

func (fx *fixture) update(t *testing.T, want string) {
	data, err := os.ReadFile(fx.file)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	i := strings.Index(s, "-- want.json --\n")
	j := i + len("-- want.json --\n")
	k := strings.Index(s[j:], "\n-- ")
	rest := ""
	if k >= 0 {
		rest = s[j+k+1:]
	}
	if err := os.WriteFile(fx.file, []byte(s[:j]+want+rest), 0o644); err != nil {
		t.Fatal(err)
	}
}
