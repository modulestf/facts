package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fb "github.com/modulestf/facts/internal/bundle"
)

const mergeFixtures = "../../testdata/merge"

// regionFile writes a fixture's bundle.json in the region form mtf-facts
// bundle writes, and returns its path.
func regionFile(t *testing.T, fixture string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mergeFixtures, fixture, "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ai bytes.Buffer
	if err := json.Compact(&ai, data); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(ai.String())
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "bundle.txt")
	region := fmt.Appendf(nil, "FACTS-BUNDLE-BYTES %d\nFACTS-BUNDLE BEGIN\n%s\nFACTS-BUNDLE END\n", len(payload), payload)
	if err := os.WriteFile(p, region, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMergeCheck(t *testing.T) {
	dir := filepath.Join(mergeFixtures, "answered-once-duplicate")
	tests := []struct {
		name     string
		reply    string
		wantCode int
		wantOut  string
	}{
		{name: "valid", reply: "model-output.repair.json", wantCode: exitReplyValid, wantOut: `{"result":"valid"}` + "\n"},
		{name: "repair", reply: "model-output.json", wantCode: exitReplyRepair,
			wantOut: `{"result":"repair","reasons":["candidate C-001 is answered more than once"]}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{"merge", "check", regionFile(t, "answered-once-duplicate"), filepath.Join(dir, tt.reply)}, &out, &errb)
			if code != tt.wantCode || out.String() != tt.wantOut || errb.Len() != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}

func TestMergeRefusesUsage(t *testing.T) {
	dir := filepath.Join(mergeFixtures, "dedup-step1")
	bundle, reply := regionFile(t, "dedup-step1"), filepath.Join(dir, "model-output.json")
	for name, args := range map[string][]string{
		"no verb":              {"merge"},
		"unknown verb":         {"merge", "apply"},
		"check one file":       {"merge", "check", bundle},
		"check three files":    {"merge", "check", bundle, reply, reply},
		"check a directory":    {"merge", "check", bundle, dir},
		"check broken bundle":  {"merge", "check", reply, reply},
		"check plain JSON":     {"merge", "check", filepath.Join(dir, "bundle.json"), reply},
		"final no registry":    {"merge", "final", bundle, reply},
		"final one file":       {"merge", "final", "--registry", reply, bundle},
		"final four files":     {"merge", "final", "--registry", reply, bundle, reply, reply, reply},
		"final bad registry":   {"merge", "final", "--registry", reply, bundle, reply},
		"final missing bundle": {"merge", "final", "--registry", reply, filepath.Join(dir, "nope.json"), reply},
		"counts one file":      {"merge", "counts", bundle},
		"counts four files":    {"merge", "counts", bundle, reply, reply, reply},
		"counts plain JSON":    {"merge", "counts", filepath.Join(dir, "bundle.json"), reply},
		"counts a directory":   {"merge", "counts", bundle, dir},
	} {
		t.Run(name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != exitError || out.Len() != 0 || errb.Len() == 0 {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}

// TestMergeCounts runs merge counts: the counts of the reply merge final
// reads, null when it has none, and an error for an invalid first reply
// without a repair reply.
func TestMergeCounts(t *testing.T) {
	dir := filepath.Join(mergeFixtures, "counts-from-repair")
	bundle := regionFile(t, "counts-from-repair")
	want, err := os.ReadFile(filepath.Join(dir, "expected-counts.json"))
	if err != nil {
		t.Fatal(err)
	}
	noCounts := filepath.Join(mergeFixtures, "answered-once-duplicate", "model-output.repair.json")
	for name, tc := range map[string]struct {
		files []string
		code  int
		out   string
	}{
		"from the repair reply": {[]string{"model-output.json", "model-output.repair.json"}, 0, string(want)},
		"from the first reply":  {[]string{"model-output.repair.json", "model-output.json"}, 0, string(want)},
		"none":                  {[]string{"model-output.json", noCounts}, 0, `{"counts":null}` + "\n"},
		"no repair reply":       {[]string{"model-output.json"}, exitError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"merge", "counts", bundle}
			for _, f := range tc.files {
				if !filepath.IsAbs(f) && !strings.Contains(f, "/") {
					f = filepath.Join(dir, f)
				}
				args = append(args, f)
			}
			var out, errb bytes.Buffer
			code := run(args, &out, &errb)
			if code != tc.code || out.String() != tc.out || (code == 0) != (errb.Len() == 0) {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}

// TestMergeFinal runs merge final on a fixture with a repair turn, with the
// flags after the files, and compares the output with the fixture's
// expected findings, ids included.
func TestMergeFinal(t *testing.T) {
	reg := os.Getenv(registryEnv)
	if reg == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	dir := filepath.Join(mergeFixtures, "repair-still-invalid")
	var out, errb bytes.Buffer
	code := run([]string{
		"merge", "final",
		regionFile(t, "repair-still-invalid"), filepath.Join(dir, "model-output.json"), filepath.Join(dir, "model-output.repair.json"),
		"--registry", reg, "--templates", filepath.Join(mergeFixtures, "registry.json"),
	}, &out, &errb)
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	want, err := os.ReadFile(filepath.Join(dir, "expected-findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got, exp any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &exp); err != nil {
		t.Fatal(err)
	}
	g, _ := json.Marshal(got)
	e, _ := json.Marshal(exp)
	if !bytes.Equal(g, e) {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
	if !strings.HasSuffix(out.String(), "]\n") {
		t.Errorf("output does not end with a newline")
	}
}

// TestMergeOnRealBundle runs the bundle mtf-facts bundle writes from a lint
// report through merge check and merge final, with that report's index.
func TestMergeOnRealBundle(t *testing.T) {
	bundleFile, report, regFile := bundleFromLint(t, "../../testdata/compat-iface-positive", "../../testdata/profile", "")
	f, err := os.Open(bundleFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := fb.Decode(f)
	if err != nil || len(b.Candidates) == 0 {
		t.Fatalf("bundle %+v, %v", b, err)
	}
	tmp := t.TempDir()
	reply := func(name, decision string) string {
		var ds []string
		for _, c := range b.Candidates {
			ds = append(ds, fmt.Sprintf(`{"candidate_id":%q,%s}`, c.CandidateID, decision))
		}
		p := filepath.Join(tmp, name)
		text := `{"candidate_decisions":[` + strings.Join(ds, ",") + `],"findings":[]}`
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	confirm := reply("confirm.json", `"decision":"confirm","reason":"r","severity":"CRITICAL","summary":"s","suggested_fix":"f"`)
	reject := reply("reject.json", `"decision":"reject","reason":"r"`)
	empty := filepath.Join(tmp, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		reply string
		code  int
	}{"confirm": {confirm, exitReplyValid}, "reject": {reject, exitReplyValid}, "empty": {empty, exitReplyRepair}} {
		t.Run("check "+name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{"merge", "check", bundleFile, tc.reply}, &out, &errb); code != tc.code || errb.Len() != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}

	final := func(t *testing.T, replies ...string) []map[string]any {
		t.Helper()
		var out, errb bytes.Buffer
		args := append([]string{"merge", "final", "--registry", regFile, "--report", report, bundleFile}, replies...)
		if code := run(args, &out, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("exit %d, stderr %q", code, errb.String())
		}
		var got []map[string]any
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	count := func(fs []map[string]any, source, rule string) int {
		n := 0
		for _, f := range fs {
			if f["source"] == source && (rule == "" || f["rule_id"] == rule) {
				n++
			}
		}
		return n
	}
	t.Run("final confirm", func(t *testing.T) {
		got := final(t, confirm)
		if n := count(got, "llm", ""); n == 0 || n > len(b.Candidates) || count(got, "tool", "") != len(b.ToolFindings) {
			t.Errorf("got %d llm and %d tool findings for %d candidates and %d tool findings",
				n, count(got, "tool", ""), len(b.Candidates), len(b.ToolFindings))
		}
	})
	t.Run("final reject", func(t *testing.T) {
		if got := final(t, reject); count(got, "llm", "") != 0 {
			t.Errorf("rejected candidates came back: %v", got)
		}
	})
	t.Run("final repair empty", func(t *testing.T) {
		if got := final(t, empty, empty); count(got, "host", "review.check-not-run") != len(b.Candidates) {
			t.Errorf("got %v, want one review.check-not-run per candidate", got)
		}
	})
}
